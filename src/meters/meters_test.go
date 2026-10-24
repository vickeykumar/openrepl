package meters

import (
	"testing"
	"time"
)

func at(t *testing.T, sec int64) {
	t.Helper()
	old := Now
	Now = func() time.Time { return time.Unix(sec, 0) }
	t.Cleanup(func() { Now = old })
}

func TestASeriesCountsInBucketsAndForgetsOldOnes(t *testing.T) {
	at(t, 1000000)
	var s Series
	s.Add(2)
	s.Add(3)
	if got := s.Sum(10); got != 5 {
		t.Fatalf("this bucket: %d", got)
	}
	Now = func() time.Time { return time.Unix(1000000+25, 0) }
	s.Add(1)
	if got := s.Sum(30); got != 6 {
		t.Errorf("30 s: %d", got)
	}
	if got := s.Sum(10); got != 1 {
		t.Errorf("10 s: %d (only the newest bucket)", got)
	}
	if got := s.PerMinute(30); got != 12 {
		t.Errorf("per minute over 30 s: %v", got)
	}
	// a long time later the same slots are reused, and the old counts are not read as new
	Now = func() time.Time { return time.Unix(1000000+Buckets*BucketSeconds, 0) }
	if got := s.Sum(30); got != 0 {
		t.Errorf("after 30 minutes: %d", got)
	}
	s.Add(7)
	if got := s.Sum(10); got != 7 {
		t.Errorf("a reused slot: %d", got)
	}
}

func TestLastGivesTheBucketsOldestFirst(t *testing.T) {
	at(t, 2000000)
	var s Series
	s.Add(1)
	Now = func() time.Time { return time.Unix(2000000+10, 0) }
	s.Add(2)
	Now = func() time.Time { return time.Unix(2000000+30, 0) }
	s.Add(4)
	got := s.Last(4)
	want := []int64{1, 2, 0, 4}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("last: %v, want %v", got, want)
		}
	}
	if len(s.Last(1000)) != Buckets {
		t.Error("more than the history was given")
	}
}

func TestNamedSeriesAndGauges(t *testing.T) {
	at(t, 3000000)
	Add("test.a", 5)
	Add("test.a", 1)
	if Get("test.a").Sum(10) != 6 || Get("test.b").Sum(10) != 0 {
		t.Error("series by name")
	}
	GaugeAdd("testg.python", 2)
	GaugeAdd("testg.node", 1)
	GaugeAdd("testg.node", -1)
	GaugeAdd("testg.java", 1)
	got := Gauges("testg.")
	if len(got) != 2 || got[0] != (Named{"java", 1}) || got[1] != (Named{"python", 2}) {
		t.Errorf("gauges: %+v (a gauge at 0 is left out, the rest sorted)", got)
	}
	if GaugeValue("testg.python") != 2 || GaugeValue("nothing") != 0 {
		t.Error("gauge values")
	}
}
