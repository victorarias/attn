// Package vtvocab scans raw terminal byte streams and counts the VT feature
// families used by internal/probetui's mirror tests: private mode
// sets/resets, alt-screen transitions, cursor addressing, OSC codes,
// terminal queries issued by the child, cursor save/restore, and the
// leftover CSI/ESC finals that don't fit a named bucket.
//
// This is a single linear scan, not a full VT parser: unrecognized CSI/ESC
// finals fall into "other" buckets keyed by final byte so nothing is
// silently dropped; DCS sequences are bucketed as XTGETTCAP-style child
// queries. Ported from the agent-mirror capture tool's analyzer.
package vtvocab

import "strings"

// Stats counts one phase's worth of VT feature usage. Field JSON tags match
// the agent-mirror capture tool's analysis.json shape.
type Stats struct {
	TotalBytes int64 `json:"totalBytes"`

	PrivateModeSet   map[string]int `json:"privateModeSet"`
	PrivateModeReset map[string]int `json:"privateModeReset"`

	AltScreenEnter int `json:"altScreenEnter"`
	AltScreenExit  int `json:"altScreenExit"`

	ED  int `json:"ed"`
	EL  int `json:"el"`
	CUP int `json:"cup"`
	SGR int `json:"sgr"`

	SMNonPrivate int `json:"smNonPrivate"`
	RMNonPrivate int `json:"rmNonPrivate"`

	OSCByCode map[string]int `json:"oscByCode"`

	// Queries issued BY the child process.
	DA1QueryFromChild      int            `json:"da1QueryFromChild"`
	CPRQueryFromChild      int            `json:"cprQueryFromChild"`
	DECRQMFromChild        int            `json:"decrqmFromChild"`
	XTGETTCAPFromChild     int            `json:"xtgettcapFromChild"`
	OSCColorQueryFromChild map[string]int `json:"oscColorQueryFromChild"`

	CursorSaveDECSC    int `json:"cursorSaveDECSC"`
	CursorRestoreDECRC int `json:"cursorRestoreDECRC"`
	CursorSaveCSIs     int `json:"cursorSaveCSI_s"`
	CursorRestoreCSIu  int `json:"cursorRestoreCSI_u"`

	DECSTBM int `json:"decstbm"`

	Newlines        int `json:"newlines"`
	CarriageReturns int `json:"carriageReturns"`

	OtherCSIFinal map[string]int `json:"otherCSIFinal"`
	OtherESCFinal map[string]int `json:"otherESCFinal"`
}

func newStats() *Stats {
	return &Stats{
		PrivateModeSet:         map[string]int{},
		PrivateModeReset:       map[string]int{},
		OSCByCode:              map[string]int{},
		OSCColorQueryFromChild: map[string]int{},
		OtherCSIFinal:          map[string]int{},
		OtherESCFinal:          map[string]int{},
	}
}

var altScreenModes = map[string]bool{"47": true, "1047": true, "1049": true}

// Analyze scans raw and returns the VT feature counts observed in it.
func Analyze(raw []byte) Stats {
	s := newStats()

	n := len(raw)
	i := 0
	for i < n {
		b := raw[i]
		if b != 0x1b {
			switch b {
			case '\n':
				s.Newlines++
			case '\r':
				s.CarriageReturns++
			}
			i++
			continue
		}

		// ESC at i. Need at least one more byte to classify.
		if i+1 >= n {
			i++
			break
		}
		esc := raw[i+1]

		switch esc {
		case '[': // CSI
			j := i + 2
			for j < n && raw[j] < 0x40 {
				j++
			}
			if j >= n {
				i = j
				break
			}
			final := raw[j]
			params := string(raw[i+2 : j])
			classifyCSI(s, params, final)
			i = j + 1

		case ']': // OSC, terminated by BEL or ST (ESC \)
			j := i + 2
			for j < n {
				if raw[j] == 0x07 {
					break
				}
				if raw[j] == 0x1b && j+1 < n && raw[j+1] == '\\' {
					break
				}
				j++
			}
			content := string(raw[i+2 : min(j, n)])
			classifyOSC(s, content)
			if j < n && raw[j] == 0x07 {
				i = j + 1
			} else if j+1 < n {
				i = j + 2
			} else {
				i = n
			}

		case 'P': // DCS, terminated by ST (ESC \); approximated as XTGETTCAP bucket
			j := i + 2
			for j < n {
				if raw[j] == 0x1b && j+1 < n && raw[j+1] == '\\' {
					break
				}
				j++
			}
			s.XTGETTCAPFromChild++
			if j+1 < n {
				i = j + 2
			} else {
				i = n
			}

		case '7':
			s.CursorSaveDECSC++
			i += 2

		case '8':
			s.CursorRestoreDECRC++
			i += 2

		default:
			s.OtherESCFinal[string(esc)]++
			i += 2
		}
	}

	s.TotalBytes = int64(n)

	return *s
}

func classifyCSI(c *Stats, params string, final byte) {
	private := strings.HasPrefix(params, "?")

	switch {
	case private && (final == 'h' || final == 'l'):
		modes := strings.Split(strings.TrimPrefix(params, "?"), ";")
		for _, m := range modes {
			if m == "" {
				continue
			}
			if final == 'h' {
				c.PrivateModeSet[m]++
				if altScreenModes[m] {
					c.AltScreenEnter++
				}
			} else {
				c.PrivateModeReset[m]++
				if altScreenModes[m] {
					c.AltScreenExit++
				}
			}
		}
		return

	case private && final == 'p' && strings.HasSuffix(params, "$"):
		c.DECRQMFromChild++
		return

	case !private && (final == 'h' || final == 'l'):
		if final == 'h' {
			c.SMNonPrivate++
		} else {
			c.RMNonPrivate++
		}
		return
	}

	switch final {
	case 'J':
		c.ED++
	case 'K':
		c.EL++
	case 'H':
		c.CUP++
	case 'm':
		c.SGR++
	case 'r':
		if !private {
			c.DECSTBM++
		} else {
			c.OtherCSIFinal[string(final)]++
		}
	case 's':
		if !private {
			c.CursorSaveCSIs++
		} else {
			c.OtherCSIFinal[string(final)]++
		}
	case 'u':
		c.CursorRestoreCSIu++
	case 'c':
		if params == "" || params == "0" {
			c.DA1QueryFromChild++
		} else {
			c.OtherCSIFinal[string(final)]++
		}
	case 'n':
		if params == "6" {
			c.CPRQueryFromChild++
		} else {
			c.OtherCSIFinal[string(final)]++
		}
	default:
		c.OtherCSIFinal[string(final)]++
	}
}

func classifyOSC(c *Stats, content string) {
	code := content
	rest := ""
	if idx := strings.IndexByte(content, ';'); idx >= 0 {
		code = content[:idx]
		rest = content[idx+1:]
	}
	c.OSCByCode[code]++

	if (code == "10" || code == "11" || code == "12") && rest == "?" {
		c.OSCColorQueryFromChild[code]++
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
