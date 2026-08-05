package crawler

import "strings"

// MapEvent converts mcpclient-style errors into crawl event types.
func MapEvent(err error) (event, detail string) {
	if err == nil {
		return EventOK, ""
	}
	s := err.Error()
	ls := strings.ToLower(s)
	switch {
	case strings.Contains(ls, "timeout") || strings.Contains(ls, "deadline"):
		return EventTimeout, s
	case strings.Contains(ls, "connection refused") || strings.Contains(ls, "refused"):
		return EventRefused, s
	case strings.Contains(ls, "malformed") || strings.Contains(ls, "invalid character"):
		return EventMalformed, s
	case strings.Contains(ls, "too large") || strings.Contains(ls, "1 mib") || strings.Contains(ls, "8 mib"):
		return EventTooLarge, s
	case strings.Contains(ls, "protocol"):
		return EventProtocol, s
	default:
		return EventTransport, s
	}
}
