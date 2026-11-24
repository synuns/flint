package ops

import "errors"

func notImplemented(task string) error {
	return errors.New("not implemented: " + task + " (wave0 stub)")
}

func notImplementedPanic(task string) {
	panic("not implemented: " + task + " (wave0 stub)")
}
