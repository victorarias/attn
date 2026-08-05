package ghosttyvt

// ColorTheme is the embedder-owned default color state. Programs may layer OSC
// overrides on top; updating these defaults must preserve those overrides.
type ColorTheme struct {
	Foreground     uint32
	Background     uint32
	Cursor         uint32
	ANSIPalette    [16]uint32
	HasForeground  bool
	HasBackground  bool
	HasCursor      bool
	HasANSIPalette bool
}
