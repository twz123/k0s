package util_test

import "io/fs"

func TestEmptyFS() {
	// Create an instance of our empty filesystem.
	var filesystem fs.FS = &emptyFS{}

	// Attempt to open a file (this will always fail in the empty filesystem).
	_, err := filesystem.Open("anyfile.txt")
	if err != nil {
		// This error is expected since our filesystem is intentionally empty.
		println("Error opening file:", err.Error())
	}
}
