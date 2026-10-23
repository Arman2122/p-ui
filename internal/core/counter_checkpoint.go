package core

import (
	"errors"
	"maps"
)

var ErrCounterCheckpoint = errors.New("invalid cumulative counter checkpoint")

type CounterCheckpoint struct {
	Epoch  string           `json:"epoch"`
	Primed bool             `json:"primed"`
	Last   map[string]int64 `json:"last"`
	Absent map[string]int   `json:"absent"`
}

func CounterFromCheckpoint(checkpoint CounterCheckpoint) (*Counter, error) {
	if len(checkpoint.Epoch) > 1024 || !checkpoint.Primed && (len(checkpoint.Last) != 0 || len(checkpoint.Absent) != 0) {
		return nil, ErrCounterCheckpoint
	}
	for key, value := range checkpoint.Last {
		if key == "" || len(key) > 1024 || value < 0 {
			return nil, ErrCounterCheckpoint
		}
	}
	for key, missing := range checkpoint.Absent {
		if _, ok := checkpoint.Last[key]; !ok || missing < 1 || missing >= baselineGrace {
			return nil, ErrCounterCheckpoint
		}
	}
	result := NewCounter()
	result.epoch, result.primed = checkpoint.Epoch, checkpoint.Primed
	maps.Copy(result.last, checkpoint.Last)
	maps.Copy(result.absent, checkpoint.Absent)
	return result, nil
}

func (c *Counter) checkpoint() CounterCheckpoint {
	return CounterCheckpoint{Epoch: c.epoch, Primed: c.primed, Last: maps.Clone(c.last), Absent: maps.Clone(c.absent)}
}

func (c *Counter) Checkpoint() CounterCheckpoint {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.checkpoint()
}

func (c *Counter) ObserveDurably(epoch string, readings map[string]int64, commit func(CounterCheckpoint, CounterCheckpoint, map[string]int64) error) (map[string]int64, error) {
	if commit == nil {
		return nil, ErrCounterCheckpoint
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	previous := c.checkpoint()
	next, err := CounterFromCheckpoint(previous)
	if err != nil {
		return nil, err
	}
	deltas := next.observe(epoch, readings)
	if err := commit(previous, next.checkpoint(), maps.Clone(deltas)); err != nil {
		return nil, err
	}
	c.last, c.absent, c.epoch, c.primed = next.last, next.absent, next.epoch, next.primed
	return deltas, nil
}
