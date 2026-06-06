//go:build !linux

package vmtest

func noFollowFlag() int {
	return 0
}
