// Package safeio provides bounded filesystem primitives for trusted startup
// inputs. Each Reader deliberately uses one worker slot: an uninterruptible
// network filesystem syscall can strand at most one goroutine in that trust
// domain, and later reads fail fast instead of accumulating workers.
package safeio

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"time"
)

const StartupReadTimeout = 2 * time.Second

var (
	ErrNotRegular  = errors.New("file is not a regular file")
	ErrTooLarge    = errors.New("file exceeds size limit")
	ErrSymlink     = errors.New("symbolic links are not allowed")
	ErrReadBusy    = errors.New("bounded file reader is occupied")
	ErrReadTimeout = errors.New("bounded file read timed out")
)

type readResult struct {
	data []byte
	err  error
}

// Reader bounds one filesystem trust domain independently. Use distinct
// instances for critical project instructions and optional metadata so a hung
// optional network mount cannot suppress a critical read.
type Reader struct {
	slot chan struct{}
}

func NewReader() *Reader {
	return &Reader{slot: make(chan struct{}, 1)}
}

var defaultReader = NewReader()

// ReadRegularFile reads at most maxBytes from path within timeout. It opens the
// path first and then validates the opened descriptor with fstat, closing the
// usual lstat/open race. A maxBytes+1 limited read catches growth after fstat.
func ReadRegularFile(path string, maxBytes int64, timeout time.Duration) ([]byte, error) {
	return defaultReader.readRegularFile(path, maxBytes, timeout, false, false)
}

func (r *Reader) ReadRegularFile(path string, maxBytes int64, timeout time.Duration) ([]byte, error) {
	return r.readRegularFile(path, maxBytes, timeout, false, false)
}

// ReadRegularFileNoFollow is for implicitly discovered inputs. Unlike an
// explicit user-selected import, these files must not redirect through a
// symlink to data outside the expected startup location.
func ReadRegularFileNoFollow(path string, maxBytes int64, timeout time.Duration) ([]byte, error) {
	return defaultReader.readRegularFile(path, maxBytes, timeout, true, false)
}

func (r *Reader) ReadRegularFileNoFollow(path string, maxBytes int64, timeout time.Duration) ([]byte, error) {
	return r.readRegularFile(path, maxBytes, timeout, true, false)
}

// ReadPrivateRegularFileNoFollow is the persistence-store variant. It applies
// owner-only permissions to the verified open descriptor, never to a path that
// could be swapped to a symlink between validation and chmod.
func (r *Reader) ReadPrivateRegularFileNoFollow(path string, maxBytes int64, timeout time.Duration) ([]byte, error) {
	return r.readRegularFile(path, maxBytes, timeout, true, true)
}

func (r *Reader) readRegularFile(path string, maxBytes int64, timeout time.Duration, noFollow, makePrivate bool) ([]byte, error) {
	if r == nil || r.slot == nil {
		return nil, fmt.Errorf("bounded file reader is not initialized")
	}
	if maxBytes <= 0 || maxBytes == math.MaxInt64 {
		return nil, fmt.Errorf("invalid regular-file size limit %d", maxBytes)
	}
	if timeout <= 0 {
		return nil, fmt.Errorf("invalid regular-file read timeout %s", timeout)
	}
	select {
	case r.slot <- struct{}{}:
	default:
		return nil, fmt.Errorf("%w: %s", ErrReadBusy, path)
	}

	result := make(chan readResult, 1)
	go func() {
		data, err := readRegularFile(path, maxBytes, noFollow, makePrivate)
		<-r.slot
		result <- readResult{data: data, err: err}
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case outcome := <-result:
		return outcome.data, outcome.err
	case <-timer.C:
		return nil, fmt.Errorf("%w after %s: %s", ErrReadTimeout, timeout, path)
	}
}

func readRegularFile(path string, maxBytes int64, noFollow, makePrivate bool) ([]byte, error) {
	var file *os.File
	var err error
	if noFollow {
		file, err = openFileNoFollow(path)
	} else {
		file, err = openFileFollow(path)
	}
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("fstat %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %s (%s)", ErrNotRegular, path, info.Mode().Type())
	}
	if makePrivate {
		if err := file.Chmod(0o600); err != nil {
			return nil, fmt.Errorf("secure open file %s: %w", path, err)
		}
	}
	if info.Size() > maxBytes {
		return nil, fmt.Errorf("%w: %s is %d bytes (limit %d)", ErrTooLarge, path, info.Size(), maxBytes)
	}

	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("%w while reading %s (limit %d)", ErrTooLarge, path, maxBytes)
	}
	return data, nil
}
