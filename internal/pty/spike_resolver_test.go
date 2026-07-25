package pty

// SPIKE — throwaway. The TTL sweep showed no single heartbeat TTL gives both a
// fast settle and no false-settle mid-tool, so the heartbeat alone is not a
// detector. This file tests the actual proposal: hook brackets as the level for
// "is work outstanding", with the heartbeat as an independent corroborator.
//
// Scoring a bracket detector against hook-derived truth is circular when every
// hook arrives, so the experiment is the failure mode that actually bites:
// DROPPED hooks (spawn failure, socket error, a hook event the agent never
// fires). We drop hooks at increasing rates and ask how much of the truth the
// heartbeat recovers.

import (
	"encoding/base64"
	"fmt"
	"math/rand"
	"testing"
)

type spikeResolver struct {
	turnOpen     bool
	toolOpen     bool
	approvalOpen bool
	autoMode     bool

	osc          *spikeOSCDetector
	useHeartbeat bool
	ttl          float64
	// staleAfter: how long a bracket may stay open with a silent heartbeat
	// before we conclude a Stop/PostToolUse was lost and settle anyway.
	staleAfter float64
}

func (r *spikeResolver) onHook(name string) {
	switch name {
	case "UserPromptSubmit":
		r.turnOpen = true
		r.approvalOpen = false
	case "PreToolUse":
		r.toolOpen = true
	case "PostToolUse":
		r.toolOpen = false
		r.approvalOpen = false
	case "PermissionRequest":
		if !r.autoMode {
			r.approvalOpen = true
		}
	case "Stop":
		r.turnOpen, r.toolOpen, r.approvalOpen = false, false, false
	}
}

func (r *spikeResolver) state(now float64) string {
	heartbeatFresh := r.useHeartbeat && now-r.osc.lastBusyAt <= r.ttl
	heartbeatSilent := now - r.osc.lastBusyAt

	// A live heartbeat is proof of work regardless of what the brackets say —
	// this is what recovers a lost UserPromptSubmit.
	if heartbeatFresh {
		return gtWorking
	}
	if r.approvalOpen {
		return gtApproval
	}
	if r.turnOpen || r.toolOpen {
		// Bracket says work is outstanding. Trust it, unless the heartbeat has
		// been silent long enough that a closing hook was clearly lost — that is
		// the un-stick, and it is only available with the heartbeat wired.
		if r.useHeartbeat && heartbeatSilent > r.staleAfter {
			return gtSettled
		}
		return gtWorking
	}
	return gtSettled
}

// TestSpikeHookDropResilience is the load-bearing experiment: how does each
// design degrade as hooks go missing?
func TestSpikeHookDropResilience(t *testing.T) {
	const sampleStep = 0.2
	const ttl = 1.0
	const staleAfter = 4.0

	dropRates := []float64{0, 0.05, 0.10, 0.25, 0.50}

	fmt.Printf("\n%-10s %22s %22s\n", "drop rate", "hooks only", "hooks + heartbeat")
	for _, drop := range dropRates {
		var bSamples, bCorrect, hSamples, hCorrect int
		bWorst, hWorst := 0.0, 0.0

		for _, dir := range spikeDirs(t) {
			cap := loadSpikeCapture(t, dir)
			if len(cap.Hooks) == 0 {
				continue
			}
			truth := groundTruth(cap)

			// Average over seeds so a single unlucky drop pattern does not decide it.
			for seed := 0; seed < 20; seed++ {
				rng := rand.New(rand.NewSource(int64(seed)))
				kept := make([]bool, len(cap.Hooks))
				for i := range cap.Hooks {
					kept[i] = rng.Float64() >= drop
				}

				bracket := &spikeResolver{autoMode: cap.AutoMode, osc: newSpikeOSCDetector(ttl)}
				hybrid := &spikeResolver{autoMode: cap.AutoMode, osc: newSpikeOSCDetector(ttl),
					useHeartbeat: true, ttl: ttl, staleAfter: staleAfter}

				chunkIdx, hookIdx := 0, 0
				var bMarks, hMarks []string
				for now := 0.0; now <= cap.End; now += sampleStep {
					for chunkIdx < len(cap.Chunks) && cap.Chunks[chunkIdx].T <= now {
						data, _ := base64.StdEncoding.DecodeString(cap.Chunks[chunkIdx].B64)
						hybrid.osc.feed(cap.Chunks[chunkIdx].T, data)
						chunkIdx++
					}
					for hookIdx < len(cap.Hooks) && cap.Hooks[hookIdx].At <= now {
						if kept[hookIdx] {
							bracket.onHook(cap.Hooks[hookIdx].Name)
							hybrid.onHook(cap.Hooks[hookIdx].Name)
						}
						hookIdx++
					}
					if now < firstHookAt(cap) {
						continue
					}
					tr := truth(now)

					bSamples++
					if bracket.state(now) == tr {
						bCorrect++
						bMarks = append(bMarks, ".")
					} else {
						bMarks = append(bMarks, "X")
					}

					hSamples++
					if hybrid.state(now) == tr {
						hCorrect++
						hMarks = append(hMarks, ".")
					} else {
						hMarks = append(hMarks, "X")
					}
				}
				if w := worstStreak(bMarks, sampleStep); w > bWorst {
					bWorst = w
				}
				if w := worstStreak(hMarks, sampleStep); w > hWorst {
					hWorst = w
				}
			}
		}
		fmt.Printf("%-10.0f%% %11.1f%% worst %5.1fs %11.1f%% worst %5.1fs\n",
			100*drop,
			100*float64(bCorrect)/float64(bSamples), bWorst,
			100*float64(hCorrect)/float64(hSamples), hWorst)
	}
}
