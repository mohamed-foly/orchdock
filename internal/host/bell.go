package host

// bellScanner finds terminal bells (BEL, 0x07) in an output stream while
// ignoring the BEL that terminates an OSC sequence such as a window-title
// update ("ESC ] 0 ; title BEL"). It keeps state between chunks because a
// sequence can be split across reads.
type bellScanner struct {
	state scanState
}

type scanState uint8

const (
	scanText    scanState = iota
	scanEsc               // after ESC
	scanOSC               // inside an OSC string: BEL terminates it
	scanOSCEsc            // ESC inside an OSC string: a following '\' terminates it
	scanString            // inside DCS/SOS/PM/APC: only ST terminates, BEL is data
	scanStringE           // ESC inside one of those
)

const (
	esc = 0x1b
	bel = 0x07
)

// Feed scans chunk and reports whether it contains a real bell.
func (s *bellScanner) Feed(chunk []byte) bool {
	rang := false
	for _, b := range chunk {
		switch s.state {
		case scanText:
			switch b {
			case esc:
				s.state = scanEsc
			case bel:
				rang = true
			}
		case scanEsc:
			s.state = scanText
			switch b {
			case ']':
				s.state = scanOSC
			case 'P', 'X', '^', '_':
				s.state = scanString
			case esc:
				s.state = scanEsc
			}
		case scanOSC:
			switch b {
			case bel:
				s.state = scanText
			case esc:
				s.state = scanOSCEsc
			}
		case scanOSCEsc:
			s.state = scanText
			switch b {
			case '\\':
			case ']':
				s.state = scanOSC
			case esc:
				s.state = scanEsc
			}
		case scanString:
			if b == esc {
				s.state = scanStringE
			}
		case scanStringE:
			if b == '\\' {
				s.state = scanText
			} else if b != esc {
				s.state = scanString
			}
		}
	}
	return rang
}
