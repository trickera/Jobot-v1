package server

import "testing"

func TestIndeedListingOnlySkipsDescriptionJitter(t *testing.T) {
	if shouldJitterBeforeDescription("Indeed") {
		t.Fatal("expected Indeed listing-only enrichment to skip the per-job detail jitter")
	}
	for _, source := range []string{"LinkedIn", "Gupy", "Remotive"} {
		if !shouldJitterBeforeDescription(source) {
			t.Fatalf("expected %s to preserve its existing description jitter", source)
		}
	}
}

// The band has to fit the search budget. At the shipped defaults the old raw
// 1-15s band averaged 8s, so a 40-job search spent ~320s asleep against a 240s
// budget — it timed out before it finished, which is how the pacing ended up
// causing the re-runs it was meant to avoid.
func TestDescriptionJitterFitsTheSearchBudget(t *testing.T) {
	config := defaultConfig()

	floor, ceiling := descriptionJitterBand(config)
	jobs := normalizedMaxJobs(config)
	meanSeconds := float64(floor+ceiling) / 2
	totalSleep := meanSeconds * float64(jobs)
	budget := float64(config.Form.SearchTimeoutSeconds)

	if totalSleep > budget*descriptionJitterBudgetShare+1 {
		t.Fatalf("expected the expected sleep (%.0fs over %d jobs) to fit inside its share of the %.0fs budget",
			totalSleep, jobs, budget)
	}
	if floor < minDescriptionJitterSeconds {
		t.Fatalf("expected a floor of at least %ds so fetches stay paced, got %d", minDescriptionJitterSeconds, floor)
	}
}

// A user who deliberately paces slowly must not be sped up behind their back.
func TestDescriptionJitterNeverExceedsTheConfiguredMax(t *testing.T) {
	config := defaultConfig()
	config.Form.MaxDelaySeconds = 2

	if _, ceiling := descriptionJitterBand(config); ceiling > 2 {
		t.Fatalf("expected the configured 2s cap to hold, got a %ds ceiling", ceiling)
	}
}

// A big search over a short budget must still leave a real gap between fetches:
// the pacing degrades, it does not vanish.
func TestDescriptionJitterKeepsAFloorWhenTheBudgetIsTight(t *testing.T) {
	config := defaultConfig()
	config.Form.MaxJobs = 200
	config.Form.SearchTimeoutSeconds = minSearchTimeoutSeconds

	floor, ceiling := descriptionJitterBand(config)

	if floor < minDescriptionJitterSeconds || ceiling < floor {
		t.Fatalf("expected a usable band even under a tight budget, got [%d, %d]", floor, ceiling)
	}
}

// A generous budget must not translate into an absurd pause per job.
func TestDescriptionJitterIsCappedOnAGenerousBudget(t *testing.T) {
	config := defaultConfig()
	config.Form.MaxJobs = 2
	config.Form.SearchTimeoutSeconds = maxSearchTimeoutSeconds
	config.Form.MaxDelaySeconds = 60

	if _, ceiling := descriptionJitterBand(config); ceiling > maxDescriptionJitterSeconds {
		t.Fatalf("expected the ceiling capped at %ds, got %d", maxDescriptionJitterSeconds, ceiling)
	}
}
