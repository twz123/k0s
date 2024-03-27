package util

import "io/fs"

func EmptyFS() fs.FS {
	return (FSFunc)(NotFound)
}
