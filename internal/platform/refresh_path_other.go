//go:build !windows

package platform

func RefreshSearchPath() error {
	return nil
}
