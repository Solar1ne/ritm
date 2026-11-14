package workoutimport

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// Summary fields follow Garmin's TCX v2 schema:
// https://www8.garmin.com/xmlschemas/TrainingCenterDatabasev2.xsd
// Track points and vendor extensions are scanned but not retained. This is a
// bounded activity-summary reader, not an XSD validator or a GPS-track importer.
const tcxNamespace = "http://www.garmin.com/xmlschemas/TrainingCenterDatabase/v2"

type tcxActivity struct {
	workout Workout
	idSeen  bool
	laps    int
	seconds float64
	meters  float64
}

type tcxLap struct {
	seconds, meters         float64
	secondsSeen, metersSeen bool
}

func parseTCX(ctx context.Context, src io.Reader, loc *time.Location) ([]Workout, error) {
	decoder := xml.NewDecoder(src)
	var path []xml.Name
	var result batch
	var activity *tcxActivity
	var lap *tcxLap
	var field string
	var value strings.Builder
	rootSeen, activitiesSeen, declarationSeen := false, false, false
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			if !rootSeen || len(path) != 0 || !activitiesSeen {
				return nil, errors.New("TCX должен содержать TrainingCenterDatabase/Activities")
			}
			return result.items, nil
		}
		if err != nil {
			var syntax *xml.SyntaxError
			if errors.As(err, &syntax) {
				return nil, fmt.Errorf("некорректный TCX XML в строке %d", syntax.Line)
			}
			if len(err.Error()) > 500 {
				return nil, errors.New("не удалось прочитать TCX XML")
			}
			return nil, fmt.Errorf("некорректный TCX XML: %w", err)
		}
		switch token := token.(type) {
		case xml.Directive:
			return nil, errors.New("DTD, ENTITY и другие XML-директивы в TCX запрещены")
		case xml.ProcInst:
			if token.Target != "xml" || rootSeen || declarationSeen {
				return nil, errors.New("XML processing instructions в TCX не поддерживаются")
			}
			declarationSeen = true
		case xml.StartElement:
			if len(path) >= MaxDepth {
				return nil, errors.New("TCX превышает глубину XML 32")
			}
			if field != "" {
				return nil, errors.New("поле сводки TCX не может содержать XML-элементы")
			}
			if err := uniqueAttributes(token.Attr); err != nil {
				return nil, err
			}
			path = append(path, token.Name)
			if len(path) == 1 {
				if rootSeen || !matches(path, "TrainingCenterDatabase") {
					return nil, errors.New("ожидается единственный TrainingCenterDatabase в пространстве имён TCX v2")
				}
				rootSeen = true
			}
			if len(path) == 2 && (token.Name.Local == "Workouts" || token.Name.Local == "Courses") {
				return nil, errors.New("TCX Courses и Workouts не поддерживаются; экспортируйте Activities")
			}
			if matches(path, "TrainingCenterDatabase", "Activities") {
				if activitiesSeen {
					return nil, errors.New("TCX содержит повторный Activities")
				}
				activitiesSeen = true
			}
			if len(path) == 3 && matches(path[:2], "TrainingCenterDatabase", "Activities") {
				if token.Name != (xml.Name{Space: tcxNamespace, Local: "Activity"}) {
					return nil, errors.New("поддерживается только TCX Activity; MultiSportSession не поддерживается")
				}
				kind, err := tcxKind(attribute(token.Attr, "Sport"))
				if err != nil {
					return nil, err
				}
				activity = &tcxActivity{workout: Workout{Kind: kind}}
			}
			if matches(path, "TrainingCenterDatabase", "Activities", "Activity", "Id") {
				if activity.idSeen {
					return nil, errors.New("в TCX Activity повторяется Id")
				}
				activity.idSeen = true
				field = "Id"
				value.Reset()
			}
			if matches(path, "TrainingCenterDatabase", "Activities", "Activity", "Lap") {
				activity.laps++
				if activity.laps > 1000 {
					return nil, errors.New("в одной TCX Activity допускается не более 1000 Lap")
				}
				if _, err := parseDate(attribute(token.Attr, "StartTime"), loc, false); err != nil {
					return nil, errors.New("TCX Lap требует StartTime в формате RFC3339")
				}
				lap = &tcxLap{}
			}
			if len(path) == 5 && matches(path[:4], "TrainingCenterDatabase", "Activities", "Activity", "Lap") && token.Name.Space == tcxNamespace {
				switch token.Name.Local {
				case "TotalTimeSeconds":
					if lap.secondsSeen {
						return nil, errors.New("в TCX Lap повторяется TotalTimeSeconds")
					}
					lap.secondsSeen = true
					field = token.Name.Local
					value.Reset()
				case "DistanceMeters":
					if lap.metersSeen {
						return nil, errors.New("в TCX Lap повторяется DistanceMeters")
					}
					lap.metersSeen = true
					field = token.Name.Local
					value.Reset()
				}
			}
		case xml.CharData:
			if field != "" {
				if value.Len()+len(token) > 128 {
					return nil, errors.New("поле сводки TCX превышает 128 байт")
				}
				value.Write(token)
			} else if len(path) == 0 && strings.TrimSpace(string(token)) != "" {
				return nil, errors.New("текст вне корневого XML-элемента")
			}
		case xml.EndElement:
			if field != "" {
				raw := strings.TrimSpace(value.String())
				switch field {
				case "Id":
					date, err := parseDate(raw, loc, false)
					if err != nil {
						return nil, errors.New("TCX Id должен быть временем RFC3339 с часовым поясом")
					}
					activity.workout.StartedAt = date
					activity.workout.ExternalID = "tcx:" + date.UTC().Format(time.RFC3339Nano)
				case "TotalTimeSeconds", "DistanceMeters":
					number, err := strconv.ParseFloat(raw, 64)
					if err != nil {
						return nil, fmt.Errorf("TCX %s должен быть числом", field)
					}
					if field == "TotalTimeSeconds" {
						if number != 0 {
							if _, err := duration(number); err != nil {
								return nil, fmt.Errorf("TCX TotalTimeSeconds: %w", err)
							}
						}
						lap.seconds = number
					} else {
						if err := validateDistance(number); err != nil {
							return nil, fmt.Errorf("TCX DistanceMeters: %w", err)
						}
						lap.meters = number
					}
				}
				field = ""
			}
			if matches(path, "TrainingCenterDatabase", "Activities", "Activity", "Lap") {
				if !lap.secondsSeen || !lap.metersSeen {
					return nil, errors.New("TCX Lap требует TotalTimeSeconds и DistanceMeters")
				}
				activity.seconds += lap.seconds
				activity.meters += lap.meters
				lap = nil
			}
			if matches(path, "TrainingCenterDatabase", "Activities", "Activity") {
				if !activity.idSeen || activity.laps == 0 {
					return nil, errors.New("TCX Activity требует Id и хотя бы один Lap")
				}
				d, err := duration(activity.seconds)
				if err != nil {
					return nil, fmt.Errorf("длительность TCX Activity: %w", err)
				}
				if err := validateDistance(activity.meters); err != nil {
					return nil, err
				}
				activity.workout.Duration = d
				meters := activity.meters
				activity.workout.DistanceMeters = &meters
				if err := result.add(activity.workout); err != nil {
					return nil, err
				}
				activity = nil
			}
			path = path[:len(path)-1]
		}
	}
}

func matches(path []xml.Name, names ...string) bool {
	if len(path) != len(names) {
		return false
	}
	for i, name := range names {
		if path[i].Space != tcxNamespace || path[i].Local != name {
			return false
		}
	}
	return true
}

func tcxKind(sport string) (string, error) {
	switch sport {
	case "Running":
		return "running", nil
	case "Biking":
		return "cycling", nil
	case "Other":
		return "other", nil
	default:
		return "", errors.New("TCX Sport должен быть Running, Biking или Other")
	}
}

func attribute(attrs []xml.Attr, name string) string {
	for _, attr := range attrs {
		if attr.Name.Space == "" && attr.Name.Local == name {
			return strings.TrimSpace(attr.Value)
		}
	}
	return ""
}

func uniqueAttributes(attrs []xml.Attr) error {
	seen := make(map[xml.Name]bool, len(attrs))
	for _, attr := range attrs {
		if seen[attr.Name] {
			return errors.New("TCX XML содержит повторный атрибут")
		}
		seen[attr.Name] = true
	}
	return nil
}
