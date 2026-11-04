package session

// EnsurePrivateDirectory creates a session directory if needed and validates
// or safely tightens its permissions without changing its owner.
func EnsurePrivateDirectory(path string) error {
	return ensurePrivateDirectory(path)
}
