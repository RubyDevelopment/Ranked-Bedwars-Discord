package database

import (
	"errors"
	"testing"
	"time"
)

func TestRetryDelay(t *testing.T) {
	want := []int{1, 1, 1, 1, 1, 2, 3, 4, 5, 5, 5}
	for n, minutes := range want {
		if got := RetryDelay(n + 1); got != time.Duration(minutes)*time.Minute {
			t.Errorf("after failure %d: %s, want %dm", n+1, got, minutes)
		}
	}
}

func TestRetryUntil(t *testing.T) {
	var waits []time.Duration
	calls := 0
	check := func() error {
		if calls++; calls <= 12 {
			return errors.New("down")
		}
		return nil
	}
	sleep := func(d time.Duration) bool { waits = append(waits, d); return true }
	if err := retryUntil(check, sleep, func(error, time.Duration) {}); err != nil || len(waits) != 12 {
		t.Fatalf("err %v, %d waits", err, len(waits))
	}
	if waits[4] != time.Minute || waits[5] != 2*time.Minute || waits[8] != 5*time.Minute || waits[11] != 5*time.Minute {
		t.Errorf("schedule %v", waits)
	}
	if err := retryUntil(func() error { return errConfig }, sleep, nil); !errors.Is(err, errConfig) {
		t.Errorf("config error was retried: %v", err)
	}
	if err := retryUntil(func() error { return errors.New("down") }, func(time.Duration) bool { return false }, func(error, time.Duration) {}); !errors.Is(err, errStopped) {
		t.Errorf("stop: %v", err)
	}
}
