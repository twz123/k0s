package process

// Windows specific implementation of [OpenHandle].
func openHandle(pid PID) (Handle, error) {
	return OpenProcHandle(pid)
}
