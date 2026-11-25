package engine

import (
	"errors"
	"time"
)

const confirmationLifetime = 5 * time.Minute

type pendingAction struct {
	createdAt time.Time
	execute   func() (Result, error)
}

func (e *Engine) queueConfirmation(input, prompt string, execute func() (string, error)) error {
	if input == "" || execute == nil {
		return errors.New("cannot queue an incomplete confirmation")
	}
	e.pendingMu.Lock()
	defer e.pendingMu.Unlock()
	if e.pending == nil {
		e.pending = make(map[string]pendingAction)
	}
	now := time.Now()
	for command, pending := range e.pending {
		if now.Sub(pending.createdAt) > confirmationLifetime {
			delete(e.pending, command)
		}
	}
	e.pending[input] = pendingAction{
		createdAt: now,
		execute: func() (Result, error) {
			output, err := execute()
			if err != nil {
				return Result{}, err
			}
			return Result{Output: output}, nil
		},
	}
	return &confirmationRequiredError{prompt: prompt}
}

func (e *Engine) executePendingConfirmation(input string) (Result, bool, error) {
	e.pendingMu.Lock()
	pending, found := e.pending[input]
	if found {
		delete(e.pending, input)
	}
	e.pendingMu.Unlock()
	if !found {
		return Result{}, false, nil
	}
	if time.Since(pending.createdAt) > confirmationLifetime {
		return Result{}, true, errors.New("confirmation expired; repeat the command to request a new confirmation")
	}
	result, err := pending.execute()
	return result, true, err
}
