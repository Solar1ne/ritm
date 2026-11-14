package workoutimport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

func moscow(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func TestCSVNormalizedReimportAndOptionalDistance(t *testing.T) {
	input := "\ufeffdate,minutes,id,kind,distance\r\n2026-10-16T23:30:00.123456789Z,45.5,watch-1,running,5200\r\n2026-10-17,30,watch-2,strength,\r\n"
	loc := moscow(t)
	first, err := Parse(context.Background(), strings.NewReader(input), CSV, loc)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Parse(context.Background(), strings.NewReader(input), CSV, loc)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 || len(second) != 2 || !Equal(first[0], second[0]) || !Equal(first[1], second[1]) {
		t.Fatalf("reimport differs: %#v / %#v", first, second)
	}
	w := first[0]
	if w.ExternalID != "csv:watch-1" || w.StartedAt.Format(time.RFC3339Nano) != "2026-10-17T02:30:00.123456789+03:00" || w.Duration != 45*time.Minute+30*time.Second || w.DistanceMeters == nil || *w.DistanceMeters != 5200 || w.Kind != "running" {
		t.Fatalf("unexpected normalized workout: %#v", w)
	}
	if first[1].DistanceMeters != nil || first[1].StartedAt.Hour() != 0 {
		t.Fatal("date-only or optional distance was not preserved")
	}
	*first[0].DistanceMeters = 0
	if *second[0].DistanceMeters != 5200 {
		t.Fatal("parse results share mutable distance")
	}
}

func TestDuplicateIDIsIdempotentOrConflict(t *testing.T) {
	header := "id,date,kind,minutes\n"
	row := "same,2026-10-17,running,45\n"
	for _, format := range []Format{CSV, TCX} {
		for _, conflict := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/conflict=%t", format, conflict), func(t *testing.T) {
				input := header + row + row
				if conflict {
					input = header + row + "same,2026-10-17,running,46\n"
				}
				if format == TCX {
					a := tcxActivityXML("2026-10-17T05:00:00Z", "Running", "1800", "5000")
					b := a
					if conflict {
						b = tcxActivityXML("2026-10-17T05:00:00Z", "Running", "1801", "5000")
					}
					input = tcxDocument(a + b)
				}
				got, err := Parse(context.Background(), strings.NewReader(input), format, moscow(t))
				if conflict {
					if !errors.Is(err, ErrConflict) || got != nil {
						t.Fatalf("want atomic conflict, got=%#v err=%v", got, err)
					}
				} else if err != nil || len(got) != 1 {
					t.Fatalf("want one deduplicated record, got=%#v err=%v", got, err)
				}
			})
		}
	}
}

func TestCSVRejectsInvalidRecordsAtomically(t *testing.T) {
	valid := "good,2026-10-17,walking,30,0\n"
	tests := map[string]string{
		"bad id":          "bad\x00id,2026-10-17,running,45,0\n",
		"empty id":        ",2026-10-17,running,45,0\n",
		"invalid utf8":    "\xff,2026-10-17,running,45,0\n",
		"long id":         strings.Repeat("a", 129) + ",2026-10-17,running,45,0\n",
		"bad date":        "bad,2026-02-30,running,45,0\n",
		"year zero":       "bad,0000-01-01,running,45,0\n",
		"no timezone":     "bad,2026-10-17T07:00:00,running,45,0\n",
		"offset hour":     "bad,2026-10-17T07:00:00+24:00,running,45,0\n",
		"offset minute":   "bad,2026-10-17T07:00:00+02:60,running,45,0\n",
		"comma seconds":   "bad,\"2026-10-17T07:00:00,5Z\",running,45,0\n",
		"lost precision":  "bad,2026-10-17T07:00:00.1234567891Z,running,45,0\n",
		"kind":            "bad,2026-10-17,flying,45,0\n",
		"nan minutes":     "bad,2026-10-17,running,NaN,0\n",
		"inf minutes":     "bad,2026-10-17,running,+Inf,0\n",
		"zero minutes":    "bad,2026-10-17,running,0,0\n",
		"negative":        "bad,2026-10-17,running,-1,0\n",
		"overflow":        "bad,2026-10-17,running,1e999,0\n",
		"over day":        "bad,2026-10-17,running,1441,0\n",
		"tiny duration":   "bad,2026-10-17,running,1e-99,0\n",
		"bad minutes":     "bad,2026-10-17,running,no,0\n",
		"nan distance":    "bad,2026-10-17,running,45,NaN\n",
		"negative meters": "bad,2026-10-17,running,45,-1\n",
		"over distance":   "bad,2026-10-17,running,45,1000001\n",
		"extra column":    "bad,2026-10-17,running,45,0,extra\n",
		"invalid quote":   "\"bad,2026-10-17,running,45,0\n",
	}
	for name, row := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := Parse(context.Background(), strings.NewReader("id,date,kind,minutes,distance\n"+valid+row), CSV, moscow(t))
			if err == nil || got != nil {
				t.Fatalf("invalid record leaked partial import: %#v, %v", got, err)
			}
		})
	}
	for _, input := range []string{"", "id,date,kind\n", "id,date,kind,minutes,unknown\n", "id,date,kind,minutes,id\n", "id,date,kind,minutes\n"} {
		if got, err := Parse(context.Background(), strings.NewReader(input), CSV, moscow(t)); err == nil || got != nil {
			t.Fatalf("accepted invalid header/empty file %q", input)
		}
	}
}

func TestInputAndRecordLimits(t *testing.T) {
	base := "id,date,kind,minutes\nfirst,2026-10-17,running,30\n"
	for _, over := range []bool{false, true} {
		size := MaxBytes
		if over {
			size = MaxBytes * 2
		}
		src := &countReader{r: strings.NewReader(base + strings.Repeat("\n", size-len(base)))}
		got, err := Parse(context.Background(), src, CSV, moscow(t))
		if src.n > MaxBytes+1 {
			t.Fatalf("read beyond bound: %d", src.n)
		}
		if over && (!errors.Is(err, ErrTooLarge) || got != nil) || !over && (err != nil || len(got) != 1) {
			t.Fatalf("size %d: got=%v err=%v", size, got, err)
		}
	}
	for _, format := range []Format{CSV, TCX} {
		for _, count := range []int{100, 101} {
			input := "id,date,kind,minutes\n" + strings.Repeat("same,2026-10-17,running,30\n", count)
			if format == TCX {
				input = tcxDocument(strings.Repeat(tcxActivityXML("2026-10-17T05:00:00Z", "Other", "1800", "0"), count))
			}
			got, err := Parse(context.Background(), strings.NewReader(input), format, moscow(t))
			if count == 101 && (!errors.Is(err, ErrTooMany) || got != nil) || count == 100 && (err != nil || len(got) != 1) {
				t.Fatalf("%s count=%d: %v, %v", format, count, got, err)
			}
		}
	}
}

func TestContextAndReaderErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := Parse(ctx, strings.NewReader(""), CSV, moscow(t)); !errors.Is(err, context.Canceled) || got != nil {
		t.Fatalf("cancellation lost: %v", err)
	}
	for _, format := range []Format{CSV, TCX} {
		readErr := errors.New("reader failed")
		if _, err := Parse(context.Background(), errorReader{readErr}, format, moscow(t)); !errors.Is(err, readErr) {
			t.Fatalf("reader error lost: %v", err)
		}
	}
	for _, format := range []Format{Format("fit"), Format("garmin-csv")} {
		if got, err := Parse(context.Background(), strings.NewReader(""), format, moscow(t)); err == nil || got != nil {
			t.Fatal("unsupported format was accepted")
		}
	}
	if _, err := Parse(context.Background(), strings.NewReader(""), CSV, nil); err == nil {
		t.Fatal("nil timezone accepted")
	}
}

func TestTCXLapTotalsTimezoneAndStableID(t *testing.T) {
	activity := tcxActivityXML("2026-10-16T23:00:00.123Z", "Biking", "900.5", "3000")
	activity = strings.Replace(activity, "</Activity>", `<Lap StartTime="2026-10-16T23:15:00Z"><TotalTimeSeconds>900</TotalTimeSeconds><DistanceMeters>3500</DistanceMeters><Track><Trackpoint><DistanceMeters>9999</DistanceMeters></Trackpoint></Track></Lap></Activity>`, 1)
	got, err := Parse(context.Background(), strings.NewReader(tcxDocument(activity)), TCX, moscow(t))
	if err != nil || len(got) != 1 {
		t.Fatalf("TCX parse: %v", err)
	}
	w := got[0]
	if w.Duration != 1800*time.Second+500*time.Millisecond || *w.DistanceMeters != 6500 || w.Kind != "cycling" || w.StartedAt.Format(time.RFC3339Nano) != "2026-10-17T02:00:00.123+03:00" || w.ExternalID != "tcx:2026-10-16T23:00:00.123Z" {
		t.Fatalf("bad TCX summary: %#v", w)
	}
	equivalentOffset := strings.ReplaceAll(activity, "2026-10-16T23:00:00.123Z", "2026-10-17T02:00:00.123+03:00")
	second, err := Parse(context.Background(), strings.NewReader(tcxDocument(equivalentOffset)), TCX, moscow(t))
	if err != nil || !Equal(w, second[0]) {
		t.Fatalf("equivalent instants change external identity: %v", err)
	}
}

func TestTCXRejectsUnsafeAndIncompleteInput(t *testing.T) {
	activity := tcxActivityXML("2026-10-17T05:00:00Z", "Running", "1800", "5000")
	valid := tcxDocument(activity)
	tests := map[string]string{
		"doctype":           `<!DOCTYPE TrainingCenterDatabase SYSTEM "https://example.invalid/remote.dtd">` + valid,
		"external entity":   `<!DOCTYPE x [<!ENTITY xxe SYSTEM "file:///etc/passwd">]>` + strings.Replace(valid, "1800", "&xxe;", 1),
		"entity":            strings.Replace(valid, "1800", "&xxe;", 1),
		"processing":        `<?xml-stylesheet href="https://example.invalid"?>` + valid,
		"root namespace":    strings.Replace(valid, tcxNamespace, "https://example.invalid/", 1),
		"no timezone":       strings.ReplaceAll(valid, "2026-10-17T05:00:00Z", "2026-10-17T05:00:00"),
		"no starttime":      strings.Replace(valid, ` StartTime="2026-10-17T05:00:00Z"`, "", 1),
		"no id":             strings.Replace(valid, "<Id>2026-10-17T05:00:00Z</Id>", "", 1),
		"duplicate id":      strings.Replace(valid, "</Id>", "</Id><Id>2026-10-17T05:00:00Z</Id>", 1),
		"duplicate attr":    strings.Replace(valid, `Sport="Running"`, `Sport="Running" Sport="Other"`, 1),
		"missing meters":    strings.Replace(valid, "<DistanceMeters>5000</DistanceMeters>", "", 1),
		"duplicate seconds": strings.Replace(valid, "</TotalTimeSeconds>", "</TotalTimeSeconds><TotalTimeSeconds>2</TotalTimeSeconds>", 1),
		"nested scalar":     strings.Replace(valid, "1800", "<Value>1800</Value>", 1),
		"long scalar":       strings.Replace(valid, "1800", strings.Repeat("1", 129), 1),
		"nan":               strings.Replace(valid, "1800", "NaN", 1),
		"infinite":          strings.Replace(valid, "5000", "INF", 1),
		"negative":          strings.Replace(valid, "5000", "-1", 1),
		"unknown sport":     strings.Replace(valid, "Running", "Swimming", 1),
		"multisport":        tcxDocument(activity + "<MultiSportSession/>"),
		"no lap":            tcxDocument(`<Activity Sport="Other"><Id>2026-10-17T05:00:00Z</Id></Activity>`),
		"second root":       valid + valid,
		"outside text":      valid + "trailing text",
		"truncated":         strings.TrimSuffix(valid, "</TrainingCenterDatabase>"),
		"courses":           strings.Replace(valid, "</TrainingCenterDatabase>", "<Courses/></TrainingCenterDatabase>", 1),
		"depth":             strings.Replace(valid, "</Activity>", "<Extensions>"+strings.Repeat("<x>", MaxDepth)+strings.Repeat("</x>", MaxDepth)+"</Extensions></Activity>", 1),
		"late bad activity": tcxDocument(activity + tcxActivityXML("2026-10-17T06:00:00Z", "Running", "-2", "0")),
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := Parse(context.Background(), strings.NewReader(input), TCX, moscow(t))
			if err == nil || got != nil {
				t.Fatalf("unsafe/incomplete TCX accepted: %#v, %v", got, err)
			}
		})
	}
	input := strings.Replace(valid, "1800", "&"+strings.Repeat("attack", 1000)+";", 1)
	if _, err := Parse(context.Background(), strings.NewReader(input), TCX, moscow(t)); err == nil || len(err.Error()) > 300 {
		t.Fatal("XML syntax error reflects unbounded source content")
	}
}

func tcxDocument(activities string) string {
	return `<TrainingCenterDatabase xmlns="` + tcxNamespace + `"><Activities>` + activities + `</Activities></TrainingCenterDatabase>`
}

func tcxActivityXML(id, sport, seconds, meters string) string {
	return `<Activity Sport="` + sport + `"><Id>` + id + `</Id><Lap StartTime="` + id + `"><TotalTimeSeconds>` + seconds + `</TotalTimeSeconds><DistanceMeters>` + meters + `</DistanceMeters></Lap></Activity>`
}

type countReader struct {
	r io.Reader
	n int
}

func (r *countReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	r.n += n
	return n, err
}

type errorReader struct{ err error }

func (r errorReader) Read([]byte) (int, error) { return 0, r.err }
