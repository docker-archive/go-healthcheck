package health

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestReturns200IfThereAreNoChecks ensures that the result code of the health
// endpoint is 200 if there are not currently registered checks.
func TestReturns200IfThereAreNoChecks(t *testing.T) {
	recorder := httptest.NewRecorder()

	req, err := http.NewRequest("GET", "https://fakeurl.com/debug/health", nil)
	if err != nil {
		t.Errorf("Failed to create request.")
	}

	StatusHandler(recorder, req)

	if recorder.Code != 200 {
		t.Errorf("Did not get a 200.")
	}
}

// TestReturns500IfThereAreErrorChecks ensures that the result code of the
// health endpoint is 500 if there are health checks with errors
func TestReturns503IfThereAreErrorChecks(t *testing.T) {
	recorder := httptest.NewRecorder()

	req, err := http.NewRequest("GET", "https://fakeurl.com/debug/health", nil)
	if err != nil {
		t.Errorf("Failed to create request.")
	}

	// Create a manual error
	Register("some_check", CheckFunc(func() error {
		return errors.New("This Check did not succeed")
	}))

	StatusHandler(recorder, req)

	if recorder.Code != 503 {
		t.Errorf("Did not get a 503.")
	}
}

// TestHealthHandler ensures that our handler implementation correct protects
// the web application when things aren't so healthy.
func TestHealthHandler(t *testing.T) {
	// clear out existing checks.
	DefaultRegistry = NewRegistry()

	// protect an http server
	handler := http.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	// wrap it in our health handler
	handler = Handler(handler)

	// use this swap check status
	updater := NewStatusUpdater()
	Register("test_check", updater)

	// now, create a test server
	server := httptest.NewServer(handler)

	checkUp := func(t *testing.T, message string) {
		resp, err := http.Get(server.URL)
		if err != nil {
			t.Fatalf("error getting success status: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("unexpected response code from server when %s: %d != %d", message, resp.StatusCode, http.StatusNoContent)
		}
		// NOTE(stevvooe): we really don't care about the body -- the format is
		// not standardized or supported, yet.
	}

	checkDown := func(t *testing.T, message string) {
		resp, err := http.Get(server.URL)
		if err != nil {
			t.Fatalf("error getting down status: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("unexpected response code from server when %s: %d != %d", message, resp.StatusCode, http.StatusServiceUnavailable)
		}
	}

	checkDown(t, "initial health check") // should be down initially, and the first check update will set to the correct state later

	// server should be up
	updater.Update(nil)
	checkUp(t, "after successful check")

	// now, we fail the health check
	updater.Update(fmt.Errorf("the server is now out of commission"))
	checkDown(t, "server should be down") // should be down

	// bring server back up
	updater.Update(nil)
	checkUp(t, "when server is back up") // now we should be back up.
}

func TestNewThresholdStatusUpdater(t *testing.T) {
	testData := []struct {
		Name           string
		CheckThreshold int
		PrepareState   func(up Updater)
		ExpectedError  error
	}{
		{
			Name:           "Not yet checked",
			CheckThreshold: 1,
			PrepareState:   func(up Updater) {},
			ExpectedError:  errors.New("not yet checked"),
		},
		{
			Name:           "Successful check",
			CheckThreshold: 1,
			PrepareState: func(up Updater) {
				up.Update(nil)
			},
		},
		{
			Name:           "Failing check",
			CheckThreshold: 1,
			PrepareState: func(up Updater) {
				up.Update(errors.New("failing check"))
			},
			ExpectedError: errors.New("failing check"),
		},
		{
			Name:           "Under threshold",
			CheckThreshold: 3,
			PrepareState: func(up Updater) {
				err := errors.New("failing check")
				up.Update(err)
				up.Update(err)
			},
			ExpectedError: errors.New("failing check"),
		},
		{
			Name:           "Reaches threshold",
			CheckThreshold: 3,
			PrepareState: func(up Updater) {
				err := errors.New("failing check")
				up.Update(err)
				up.Update(err)
				up.Update(err)
			},
			ExpectedError: errors.New("failing check"),
		},
		{
			Name:           "Reaches threshold, then succeeds",
			CheckThreshold: 3,
			PrepareState: func(up Updater) {
				err := errors.New("failing check")
				up.Update(err)
				up.Update(err)
				up.Update(err)
				up.Update(nil)
			},
		},
		{
			Name:           "Count resets with success",
			CheckThreshold: 3,
			PrepareState: func(up Updater) {
				err := errors.New("failing check")
				up.Update(err)
				up.Update(err)
				up.Update(err)
				up.Update(nil)
				// We fail twice more, but this isn't enough to push passed the threshold again (since the success reset the count)
				up.Update(err)
				up.Update(err)
			},
		},
	}

	for _, d := range testData {
		t.Run(d.Name, func(t *testing.T) {
			up := NewThresholdStatusUpdater(d.CheckThreshold)

			d.PrepareState(up)

			err := up.Check()

			if d.ExpectedError != nil {
				if err == nil || d.ExpectedError.Error() != err.Error() {
					t.Fatalf("Expected [%+v] but found [%+v]", d.ExpectedError, err)
				}
			} else if d.ExpectedError == nil && err != nil {
				t.Fatalf("Check failed: %+v", err)
			}
		})
	}
}

func TestPeriodicChecker(t *testing.T) {
	okFunc := func() error { return nil }
	errFunc := func() error { return errors.New("failing check") }

	testData := []struct {
		Name          string
		CheckFunc     CheckFunc
		CheckPeriod   time.Duration
		VerifyAfter   time.Duration
		ExpectedError error
	}{
		{
			Name:          "Not yet checked",
			CheckFunc:     okFunc,
			CheckPeriod:   5 * time.Second,
			VerifyAfter:   10 * time.Millisecond,
			ExpectedError: errors.New("not yet checked"),
		},
		{
			Name:        "Successful check",
			CheckFunc:   okFunc,
			CheckPeriod: 5 * time.Millisecond,
			VerifyAfter: 100 * time.Millisecond,
		},
		{
			Name:          "Failing check",
			CheckFunc:     errFunc,
			CheckPeriod:   5 * time.Millisecond,
			VerifyAfter:   100 * time.Millisecond,
			ExpectedError: errors.New("failing check"),
		},
		{
			Name:          "Fail from 3rd check onwards",
			CheckFunc:     succeedUntil(3, func() error { return errors.New("delayed failure") }),
			CheckPeriod:   5 * time.Millisecond,
			VerifyAfter:   100 * time.Millisecond,
			ExpectedError: errors.New("delayed failure"),
		},
	}

	for _, d := range testData {
		t.Run(d.Name, func(t *testing.T) {
			pc := PeriodicChecker(d.CheckFunc, d.CheckPeriod)

			<-time.After(d.VerifyAfter)

			err := pc.Check()

			if d.ExpectedError != nil {
				if err == nil || d.ExpectedError.Error() != err.Error() {
					t.Fatalf("Expected [%+v] but found [%+v]", d.ExpectedError, err)
				}
			} else if d.ExpectedError == nil && err != nil {
				t.Fatalf("Check failed: %+v", err)
			}
		})
	}
}

func TestNewPeriodicThresholdChecker(t *testing.T) {
	okFunc := func() error { return nil }
	errFunc := func() error { return errors.New("failing check") }
	// Health check that will fail regularly, but never enough in a row to reach the failure threshold
	underThresholdCheck := func(threshold int) CheckFunc {
		// Set the initial failure count to the threshold, as we need to clear the initial check state with an immediate success before we continue
		failCount := threshold
		maxFailures := threshold - 1
		return func() error {
			if failCount < maxFailures {
				failCount++
				return fmt.Errorf("fail [%d] threshold [%d]", failCount, threshold)
			}

			failCount = 0
			return nil
		}
	}

	testData := []struct {
		Name           string
		CheckFunc      CheckFunc
		CheckPeriod    time.Duration
		CheckThreshold int
		VerifyAfter    time.Duration
		VerifyTimes    int
		ExpectedError  error
	}{
		{
			Name:           "Not yet checked",
			CheckFunc:      okFunc,
			CheckPeriod:    5 * time.Second,
			CheckThreshold: 3,
			VerifyAfter:    10 * time.Millisecond,
			VerifyTimes:    1,
			ExpectedError:  errors.New("not yet checked"),
		},
		{
			Name:           "Always successful check",
			CheckFunc:      okFunc,
			CheckPeriod:    5 * time.Millisecond,
			VerifyAfter:    100 * time.Millisecond,
			VerifyTimes:    1,
			CheckThreshold: 3,
		},
		{
			Name:           "Always failing check",
			CheckFunc:      errFunc,
			CheckPeriod:    5 * time.Millisecond,
			CheckThreshold: 3,
			VerifyAfter:    100 * time.Millisecond,
			VerifyTimes:    1,
			ExpectedError:  errors.New("failing check"),
		},
		// Due to the nature of a periodic threshold check, verifying proper behaviour around the threshold / reset is
		// difficult. To increase confidence in correctness, we do the following:
		// * Run verification multiple times
		// * Slow the check period relative to the assert frequency (ensuring fewer checks between assertions)
		// * Use a threshold of 2 such that we would reach the threshold as often as possible
		{
			Name:           "Always under threshold",
			CheckFunc:      underThresholdCheck(2),
			CheckPeriod:    20 * time.Millisecond,
			CheckThreshold: 2,
			VerifyAfter:    50 * time.Millisecond,
			VerifyTimes:    10,
		},
	}

	for _, d := range testData {
		t.Run(d.Name, func(t *testing.T) {
			pc := PeriodicThresholdChecker(d.CheckFunc, d.CheckPeriod, d.CheckThreshold)

			if d.VerifyTimes <= 0 {
				t.Fatalf("Verify times [%v] must be at least 1", d.VerifyTimes)
			}
			for i := 0; i < d.VerifyTimes; i++ {
				<-time.After(d.VerifyAfter)

				err := pc.Check()

				if d.ExpectedError != nil {
					if err == nil || d.ExpectedError.Error() != err.Error() {
						t.Fatalf("Expected [%+v] but found [%+v]", d.ExpectedError, err)
					}
				} else if d.ExpectedError == nil && err != nil {
					t.Fatalf("Check failed: %+v", err)
				}
			}
		})
	}
}

func succeedUntil(checkCount int, then CheckFunc) CheckFunc {
	check := 0
	return func() error {
		if check < checkCount {
			check++
			return nil
		}
		return then()
	}
}
