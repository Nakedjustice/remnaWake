package textutil

// Truncate shortens s to at most n bytes, appending "..." when it had to cut.
func Truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
