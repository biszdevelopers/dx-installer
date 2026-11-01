package regional

import (
	"errors"
	"os"
	"strings"
	"time"

	"developers-club-dx/internal/platform"
)

type Region string

const (
	Unknown       Region = "unknown"
	MainlandChina Region = "mainland-china"
	Global        Region = "global"
	Conflict      Region = "conflicting-signals"
)

type Signal struct {
	Source string
	Value  string
	Region Region
}

type Result struct {
	Region  Region
	Signals []Signal
}

type Preference string

var (
	ErrConflictingSignals = errors.New("regional signals conflict; select Mainland China or Global manually")
	ErrUnknownPreference  = errors.New("source region preference is invalid")
)

const (
	Automatic Preference = "automatic"
	China     Preference = "mainland-china"
	Worldwide Preference = "global"
)

func Detect(machine platform.Info) Result {
	signals := make([]Signal, 0, 5)
	add := func(source, value string, classify func(string) Region) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if region := classify(value); region != Unknown {
			signals = append(signals, Signal{Source: source, Value: value, Region: region})
		}
	}
	add("operating-system-region", operatingSystemRegion(machine), classifyLocale)
	for _, name := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		add("locale/"+name, os.Getenv(name), classifyLocale)
	}
	timezone := os.Getenv("TZ")
	if timezone == "" {
		timezone = time.Now().Location().String()
	}
	add("time-zone", timezone, classifyTimezone)

	china, global := false, false
	for _, signal := range signals {
		china = china || signal.Region == MainlandChina
		global = global || signal.Region == Global
	}
	region := Unknown
	switch {
	case china && global:
		region = Conflict
	case china:
		region = MainlandChina
	case global:
		region = Global
	}
	return Result{Region: region, Signals: signals}
}

func (result Result) UseRegionalFallback(preference Preference) (bool, error) {
	switch preference {
	case Automatic:
		if result.Region == Conflict {
			return false, ErrConflictingSignals
		}
		return result.Region == MainlandChina, nil
	case China:
		return true, nil
	case Worldwide:
		return false, nil
	default:
		return false, ErrUnknownPreference
	}
}

func classifyLocale(value string) Region {
	locale := strings.ToLower(strings.TrimSpace(value))
	if locale == "c" || locale == "posix" || locale == "" {
		return Unknown
	}
	locale = strings.ReplaceAll(locale, "-", "_")
	parts := strings.Split(locale, "_")
	if len(parts) < 2 {
		return Unknown
	}
	region := strings.SplitN(parts[1], ".", 2)[0]
	switch strings.ToUpper(region) {
	case "CN":
		return MainlandChina
	case "US", "CA", "GB", "AU", "NZ", "JP", "KR", "TW", "HK", "MO", "SG", "IN", "DE", "FR", "ES", "IT", "BR", "MX", "RU", "NL", "SE", "NO", "DK", "FI", "PL", "CH", "AT", "IE", "ZA":
		return Global
	default:
		return Unknown
	}
}

func classifyTimezone(value string) Region {
	zone := strings.ToLower(strings.TrimSpace(value))
	if zone == "prc" || strings.HasPrefix(zone, "asia/shanghai") || strings.HasPrefix(zone, "asia/chongqing") || strings.HasPrefix(zone, "asia/urumqi") || strings.HasPrefix(zone, "asia/harbin") || strings.HasPrefix(zone, "asia/beijing") {
		return MainlandChina
	}
	if strings.Contains(zone, "/") || zone == "utc" || zone == "gmt" || zone == "etc/utc" {
		return Global
	}
	return Unknown
}
