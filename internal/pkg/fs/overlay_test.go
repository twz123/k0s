package fs_test


func TestOverlayFS(t *testing.T) {

    // Usage example
    baseFS := ... // Initialize your base filesystem
    overlayFS := NewOverlayFS(baseFS)
    overlayFS.AddOverlay("/mount1", ... /* your overlay FS for /mount1 */)
    overlayFS.AddOverlay("/mount2", ... /* your overlay FS for /mount2 */)

    // Now you can use overlayFS.ReadDir("/path/to/directory") for directory listings
}
