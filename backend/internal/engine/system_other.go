//go:build !windows && !linux

package engine

func platformStats() systemStats {
	return baseStats()
}
