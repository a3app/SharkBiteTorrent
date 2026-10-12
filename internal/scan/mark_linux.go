package scan

// MarkDownloaded does nothing on Linux: there's no system-wide equivalent of
// Windows' Mark of the Web or macOS quarantine that anything checks.
func MarkDownloaded(string) {}
