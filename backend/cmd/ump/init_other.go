//go:build !linux

package main

// becomeSubreaper is a no-op outside Linux, where ump only runs for development and tests
func becomeSubreaper() {}
