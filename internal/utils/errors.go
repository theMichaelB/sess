package utils

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

var (
	ErrSessionExists    = errors.New("session already exists")
	ErrSessionNotFound  = errors.New("session not found")
	ErrSessionDead      = errors.New("session is dead")
	ErrAlreadyAttached  = errors.New("already attached to this session")
	ErrNotInSession     = errors.New("not in a session")
	ErrInSession        = errors.New("already in a session")
	ErrConnectionFailed = errors.New("connection failed")
	ErrTimeout          = errors.New("operation timed out")
)

func IsRecoverable(err error) bool {
	if err == nil {
		return true
	}

	switch {
	case errors.Is(err, syscall.EINTR):
		return true
	case errors.Is(err, syscall.EAGAIN):
		return true
	case errors.Is(err, syscall.EWOULDBLOCK):
		return true
	case errors.Is(err, os.ErrDeadlineExceeded):
		return true
	default:
		return false
	}
}

func WrapError(err error, msg string) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", msg, err)
}

type SafeRunner struct {
	maxRetries int
	onError    func(error)
}

func NewSafeRunner(maxRetries int, onError func(error)) *SafeRunner {
	return &SafeRunner{
		maxRetries: maxRetries,
		onError:    onError,
	}
}

func (r *SafeRunner) Run(fn func() error) error {
	var lastErr error

	for i := 0; i <= r.maxRetries; i++ {
		err := fn()
		if err == nil {
			return nil
		}

		lastErr = err

		if !IsRecoverable(err) {
			if r.onError != nil {
				r.onError(err)
			}
			return err
		}

		if i < r.maxRetries && r.onError != nil {
			r.onError(fmt.Errorf("retry %d/%d: %w", i+1, r.maxRetries, err))
		}
	}

	return lastErr
}

func HandlePanic(component string) {
	if r := recover(); r != nil {
		fmt.Fprintf(os.Stderr, "Panic in %s: %v\n", component, r)
		os.Exit(1)
	}
}

func FileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func EnsureDir(path string, perm os.FileMode) error {
	if err := os.MkdirAll(path, perm); err != nil {
		return WrapError(err, "failed to create directory")
	}

	stat, err := os.Stat(path)
	if err != nil {
		return WrapError(err, "failed to stat directory")
	}

	if !stat.IsDir() {
		return fmt.Errorf("%s exists but is not a directory", path)
	}

	if stat.Mode().Perm() != perm {
		if err := os.Chmod(path, perm); err != nil {
			return WrapError(err, "failed to set directory permissions")
		}
	}

	return nil
}
