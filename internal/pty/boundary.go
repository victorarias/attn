package pty

import "unicode/utf8"

// findSafeBoundary returns the largest prefix that can be safely emitted.
// It avoids splitting incomplete UTF-8 runes and incomplete ANSI escape sequences.
func findSafeBoundary(data []byte) int {
	n := len(data)
	if n == 0 {
		return 0
	}

	boundary := n

	// Protect incomplete UTF-8 rune at end.
	start := n - 4
	if start < 0 {
		start = 0
	}
	for i := n - 1; i >= start; i-- {
		if utf8.RuneStart(data[i]) {
			if !utf8.FullRune(data[i:n]) {
				boundary = i
			}
			break
		}
	}

	// Protect incomplete ANSI escape sequence at end.
	ansiBoundary := findIncompleteEscapeStart(data[:boundary])
	if ansiBoundary >= 0 && ansiBoundary < boundary {
		boundary = ansiBoundary
	}

	if boundary < 0 {
		return 0
	}
	if boundary > n {
		return n
	}
	return boundary
}

func findIncompleteEscapeStart(data []byte) int {
	n := len(data)
	if n == 0 {
		return -1
	}
	searchStart := n - 64
	if searchStart < 0 {
		searchStart = 0
	}
	for i := n - 1; i >= searchStart; i-- {
		if data[i] != 0x1b {
			continue
		}
		if isCompleteEscape(data[i:]) {
			return -1
		}
		return i
	}
	return -1
}

// isCompleteEscape returns true when bytes starting at ESC contain a full sequence.
func isCompleteEscape(seq []byte) bool {
	if len(seq) == 0 || seq[0] != 0x1b {
		return true
	}
	if len(seq) == 1 {
		return false
	}

	next := seq[1]
	switch next {
	case '[': // CSI
		for i := 2; i < len(seq); i++ {
			b := seq[i]
			if b >= 0x40 && b <= 0x7e {
				return true
			}
		}
		return false
	case ']': // OSC, terminated by BEL or ST (ESC \\\)
		for i := 2; i < len(seq); i++ {
			if seq[i] == 0x07 {
				return true
			}
			if seq[i] == 0x1b && i+1 < len(seq) && seq[i+1] == '\\' {
				return true
			}
		}
		return false
	case 'P', '^', '_': // DCS/PM/APC terminated by ST
		for i := 2; i < len(seq); i++ {
			if seq[i] == 0x1b && i+1 < len(seq) && seq[i+1] == '\\' {
				return true
			}
		}
		return false
	default:
		// 2-byte escape or charset sequence (e.g. ESC ( B)
		if next >= 0x20 && next <= 0x2f {
			if len(seq) < 3 {
				return false
			}
			final := seq[2]
			return final >= 0x30 && final <= 0x7e
		}
		return true
	}
}
