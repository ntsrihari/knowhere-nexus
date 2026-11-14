package contract

import (
	"errors"
	"regexp"
	"strconv"
)

// Version is a MAJOR.MINOR.PATCH contract version.
type Version struct{ Major, Minor, Patch int }

var semverRe = regexp.MustCompile(`^(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})\.(0|[1-9][0-9]{0,5})$`)

// ParseVersion parses a strict MAJOR.MINOR.PATCH string (no prefixes, no
// leading zeros, no pre-release or build suffixes).
func ParseVersion(s string) (Version, error) {
	m := semverRe.FindStringSubmatch(s)
	if m == nil {
		return Version{}, errors.New("schemaVersion must be MAJOR.MINOR.PATCH")
	}
	maj, _ := strconv.Atoi(m[1])
	min, _ := strconv.Atoi(m[2])
	pat, _ := strconv.Atoi(m[3])
	return Version{maj, min, pat}, nil
}

func (v Version) String() string {
	return strconv.Itoa(v.Major) + "." + strconv.Itoa(v.Minor) + "." + strconv.Itoa(v.Patch)
}

// mode is how a document of a given version is validated.
type mode int

const (
	strict  mode = iota // known version: unknown fields are errors
	lenient             // newer MINOR: unknown fields are ignored (R-17)
)

// support describes the newest version Nexus fully knows for a contract.
type support struct{ Major, Minor int }

// modeFor applies R-17: same MAJOR is required; a MINOR at or below the known
// one is validated strictly; a newer MINOR is additive and validated leniently.
func (s support) modeFor(v Version) (mode, error) {
	if v.Major != s.Major {
		return 0, &Error{Code: CodeUnsupportedVersion, Problems: []string{
			"schemaVersion " + v.String() + " is not supported (supported major version " + strconv.Itoa(s.Major) + ")"}}
	}
	if v.Minor <= s.Minor {
		return strict, nil
	}
	return lenient, nil
}
