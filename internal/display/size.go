package display

import "fmt"

func ApproximateSize(bytes int64) string {
	if bytes <= 0 {
		return "unknown"
	}
	return fmt.Sprintf("about %.0f MB", float64(bytes)/1_000_000)
}

func DownloadedSize(bytes int64) string {
	if bytes < 0 {
		bytes = 0
	}
	if bytes < 1_000_000 {
		return fmt.Sprintf("%.0f kB", float64(bytes)/1_000)
	}
	if bytes < 1_000_000_000 {
		return fmt.Sprintf("%.1f MB", float64(bytes)/1_000_000)
	}
	return fmt.Sprintf("%.2f GB", float64(bytes)/1_000_000_000)
}

func DownloadProgress(bytesReceived, totalBytes int64) string {
	received := DownloadedSize(bytesReceived)
	if totalBytes <= 0 {
		return received + " downloaded"
	}
	percent := bytesReceived * 100 / totalBytes
	if percent > 100 {
		percent = 100
	}
	return fmt.Sprintf("%s of %s (%d%%)", received, DownloadedSize(totalBytes), percent)
}
