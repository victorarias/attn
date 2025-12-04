package daemon

import (
	"encoding/json"
	"log"
	"net"
	"os"
	"time"

	"github.com/victorarias/claude-manager/internal/github"
	"github.com/victorarias/claude-manager/internal/logging"
	"github.com/victorarias/claude-manager/internal/protocol"
	"github.com/victorarias/claude-manager/internal/store"
)

// Daemon manages Claude sessions
type Daemon struct {
	socketPath string
	store      *store.Store
	listener   net.Listener
	done       chan struct{}
	logger     *logging.Logger
	ghFetcher  *github.Fetcher
}

// New creates a new daemon
func New(socketPath string) *Daemon {
	logger, _ := logging.New(logging.DefaultLogPath())
	return &Daemon{
		socketPath: socketPath,
		store:      store.NewWithPersistence(store.DefaultStatePath()),
		done:       make(chan struct{}),
		logger:     logger,
		ghFetcher:  github.NewFetcher(),
	}
}

// NewForTesting creates a daemon with a non-persistent store for tests
func NewForTesting(socketPath string) *Daemon {
	return &Daemon{
		socketPath: socketPath,
		store:      store.New(),
		done:       make(chan struct{}),
		logger:     nil, // No logging in tests
	}
}

// Start starts the daemon
func (d *Daemon) Start() error {
	// Remove stale socket
	os.Remove(d.socketPath)

	listener, err := net.Listen("unix", d.socketPath)
	if err != nil {
		return err
	}
	d.listener = listener
	d.log("daemon started")

	// Start PR polling
	go d.pollPRs()

	for {
		select {
		case <-d.done:
			return nil
		default:
		}

		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-d.done:
				return nil
			default:
				log.Printf("accept error: %v", err)
				continue
			}
		}

		go d.handleConnection(conn)
	}
}

// Stop stops the daemon
func (d *Daemon) Stop() {
	d.log("daemon stopping")
	close(d.done)
	if d.listener != nil {
		d.listener.Close()
	}
	os.Remove(d.socketPath)
	if d.logger != nil {
		d.logger.Close()
	}
}

func (d *Daemon) log(msg string) {
	if d.logger != nil {
		d.logger.Info(msg)
	}
}

func (d *Daemon) logf(format string, args ...interface{}) {
	if d.logger != nil {
		d.logger.Infof(format, args...)
	}
}

func (d *Daemon) handleConnection(conn net.Conn) {
	defer conn.Close()

	// Read message
	buf := make([]byte, 65536)
	n, err := conn.Read(buf)
	if err != nil {
		return
	}

	cmd, msg, err := protocol.ParseMessage(buf[:n])
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}

	switch cmd {
	case protocol.CmdRegister:
		d.handleRegister(conn, msg.(*protocol.RegisterMessage))
	case protocol.CmdUnregister:
		d.handleUnregister(conn, msg.(*protocol.UnregisterMessage))
	case protocol.CmdState:
		d.handleState(conn, msg.(*protocol.StateMessage))
	case protocol.CmdTodos:
		d.handleTodos(conn, msg.(*protocol.TodosMessage))
	case protocol.CmdQuery:
		d.handleQuery(conn, msg.(*protocol.QueryMessage))
	case protocol.CmdHeartbeat:
		d.handleHeartbeat(conn, msg.(*protocol.HeartbeatMessage))
	case protocol.CmdMute:
		d.handleMute(conn, msg.(*protocol.MuteMessage))
	case protocol.CmdQueryPRs:
		d.handleQueryPRs(conn, msg.(*protocol.QueryPRsMessage))
	case protocol.CmdMutePR:
		d.handleMutePR(conn, msg.(*protocol.MutePRMessage))
	default:
		d.sendError(conn, "unknown command")
	}
}

func (d *Daemon) handleRegister(conn net.Conn, msg *protocol.RegisterMessage) {
	session := &protocol.Session{
		ID:         msg.ID,
		Label:      msg.Label,
		Directory:  msg.Dir,
		TmuxTarget: msg.Tmux,
		State:      protocol.StateWaiting,
		StateSince: time.Now(),
		LastSeen:   time.Now(),
	}
	d.store.Add(session)
	d.sendOK(conn)
}

func (d *Daemon) handleUnregister(conn net.Conn, msg *protocol.UnregisterMessage) {
	d.store.Remove(msg.ID)
	d.sendOK(conn)
}

func (d *Daemon) handleState(conn net.Conn, msg *protocol.StateMessage) {
	d.store.UpdateState(msg.ID, msg.State)
	d.store.Touch(msg.ID)
	d.sendOK(conn)
}

func (d *Daemon) handleTodos(conn net.Conn, msg *protocol.TodosMessage) {
	d.store.UpdateTodos(msg.ID, msg.Todos)
	d.store.Touch(msg.ID)
	d.sendOK(conn)
}

func (d *Daemon) handleQuery(conn net.Conn, msg *protocol.QueryMessage) {
	sessions := d.store.List(msg.Filter)
	resp := protocol.Response{
		OK:       true,
		Sessions: sessions,
	}
	json.NewEncoder(conn).Encode(resp)
}

func (d *Daemon) handleHeartbeat(conn net.Conn, msg *protocol.HeartbeatMessage) {
	d.store.Touch(msg.ID)
	d.sendOK(conn)
}

func (d *Daemon) handleMute(conn net.Conn, msg *protocol.MuteMessage) {
	d.store.ToggleMute(msg.ID)
	d.sendOK(conn)
}

func (d *Daemon) handleQueryPRs(conn net.Conn, msg *protocol.QueryPRsMessage) {
	prs := d.store.ListPRs(msg.Filter)
	resp := protocol.Response{
		OK:  true,
		PRs: prs,
	}
	json.NewEncoder(conn).Encode(resp)
}

func (d *Daemon) handleMutePR(conn net.Conn, msg *protocol.MutePRMessage) {
	d.store.ToggleMutePR(msg.ID)
	d.sendOK(conn)
}

func (d *Daemon) sendOK(conn net.Conn) {
	resp := protocol.Response{OK: true}
	json.NewEncoder(conn).Encode(resp)
}

func (d *Daemon) sendError(conn net.Conn, errMsg string) {
	resp := protocol.Response{OK: false, Error: errMsg}
	json.NewEncoder(conn).Encode(resp)
}

func (d *Daemon) pollPRs() {
	if d.ghFetcher == nil || !d.ghFetcher.IsAvailable() {
		d.log("gh CLI not available, PR polling disabled")
		return
	}

	d.log("PR polling started (90s interval)")

	// Initial poll
	d.doPRPoll()

	ticker := time.NewTicker(90 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-d.done:
			return
		case <-ticker.C:
			d.doPRPoll()
		}
	}
}

func (d *Daemon) doPRPoll() {
	prs, err := d.ghFetcher.FetchAll()
	if err != nil {
		d.logf("PR poll error: %v", err)
		return
	}

	// Filter: skip muted PRs that were polled within 24h
	existingPRs := d.store.ListPRs("")
	mutedRecently := make(map[string]bool)
	for _, pr := range existingPRs {
		if pr.Muted && time.Since(pr.LastPolled) < 24*time.Hour {
			mutedRecently[pr.ID] = true
		}
	}

	var activePRs []*protocol.PR
	for _, pr := range prs {
		if !mutedRecently[pr.ID] {
			activePRs = append(activePRs, pr)
		}
	}

	d.store.SetPRs(activePRs)

	waiting := 0
	for _, pr := range activePRs {
		if pr.State == protocol.StateWaiting {
			waiting++
		}
	}
	d.logf("PR poll: %d PRs (%d waiting)", len(activePRs), waiting)
}
