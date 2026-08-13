package transcript

// AssistantMessage is one canonical assistant event annotations can address.
type AssistantMessage struct {
	Key     string
	Content string
}

// AssistantWindowLimits bound the rolling live window. Oversize messages are
// dropped whole because truncating their text would invalidate stored offsets.
type AssistantWindowLimits struct {
	MaxMessages     int
	MaxMessageChars int
	MaxTotalChars   int
}

// AssistantWindowReport names what the rolling window omitted.
type AssistantWindowReport struct {
	DroppedOversize int
	LargestDropped  int
	DroppedOld      int
	OmittedPrefix   bool
}

func (r AssistantWindowReport) Truncated() bool {
	return r.OmittedPrefix || r.DroppedOversize > 0 || r.DroppedOld > 0
}

// AssistantWindow incrementally retains the newest canonical assistant events.
type AssistantWindow struct {
	limits   AssistantWindowLimits
	messages []AssistantMessage
	report   AssistantWindowReport
}

func NewAssistantWindow(limits AssistantWindowLimits) *AssistantWindow {
	return &AssistantWindow{limits: limits}
}

// MarkPrefixOmitted records that following began in a bounded tail rather than
// at byte zero. The exact number of older messages is deliberately unknown:
// counting them would require the full-file scan this window replaces.
func (w *AssistantWindow) MarkPrefixOmitted() {
	w.report.OmittedPrefix = true
}

// Apply returns true when the externally visible snapshot changed.
func (w *AssistantWindow) Apply(events []Event) bool {
	changed := false
	for _, event := range events {
		if event.Kind != EventKindAssistant || event.Text == "" {
			continue
		}
		changed = true
		if w.limits.MaxMessageChars > 0 && len(event.Text) > w.limits.MaxMessageChars {
			w.report.DroppedOversize++
			if len(event.Text) > w.report.LargestDropped {
				w.report.LargestDropped = len(event.Text)
			}
			continue
		}
		w.messages = append(w.messages, AssistantMessage{Key: event.Cursor, Content: event.Text})
	}
	if !changed {
		return false
	}
	w.cap()
	return true
}

func (w *AssistantWindow) cap() {
	if w.limits.MaxMessages > 0 && len(w.messages) > w.limits.MaxMessages {
		dropped := len(w.messages) - w.limits.MaxMessages
		w.report.DroppedOld += dropped
		w.messages = append(w.messages[:0], w.messages[dropped:]...)
	}
	if w.limits.MaxTotalChars <= 0 {
		return
	}
	total := 0
	keepFrom := len(w.messages)
	for i := len(w.messages) - 1; i >= 0; i-- {
		if total+len(w.messages[i].Content) > w.limits.MaxTotalChars {
			break
		}
		total += len(w.messages[i].Content)
		keepFrom = i
	}
	if keepFrom > 0 {
		w.report.DroppedOld += keepFrom
		w.messages = append(w.messages[:0], w.messages[keepFrom:]...)
	}
}

func (w *AssistantWindow) Snapshot() ([]AssistantMessage, AssistantWindowReport) {
	messages := append([]AssistantMessage(nil), w.messages...)
	return messages, w.report
}
