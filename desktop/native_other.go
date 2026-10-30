//go:build !darwin

package main

// Elsewhere the desktop has no badge to set; a notification carries the
// sound where the system gives it one.
func setBadge(string)  {}
func playSound(string) {}
