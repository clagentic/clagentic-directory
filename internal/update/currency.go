package update

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"strings"
)

// recordSuffix names the install record kept beside the installed binary.
const recordSuffix = ".source"

// installRecord is what update remembers about the binary it installed. The
// binary bytes are not a usable currency key: a build is compared on the
// source commit it came from, and the digest only proves the installed file is
// still the one that was recorded.
type installRecord struct {
	Commit  string
	Package string
	SHA256  string
}

func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (r installRecord) render() []byte {
	return []byte("commit=" + r.Commit + "\npackage=" + r.Package + "\nsha256=" + r.SHA256 + "\n")
}

func parseInstallRecord(data []byte) installRecord {
	var r installRecord
	for _, line := range strings.Split(string(data), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch k {
		case "commit":
			r.Commit = v
		case "package":
			r.Package = v
		case "sha256":
			r.SHA256 = v
		}
	}
	return r
}

// readInstallRecord returns the record at path, or the zero record when none
// exists. A zero record never matches, so a host without one is rebuilt.
func readInstallRecord(d Deps, path string) (installRecord, error) {
	data, err := d.FS.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return installRecord{}, nil
	}
	if err != nil {
		return installRecord{}, err
	}
	return parseInstallRecord(data), nil
}

// current reports whether the installed binary is the build of want: same
// resolved commit and package as recorded, and the installed bytes still hash
// to the recorded digest. The commit comes from a fresh fetch, never from the
// record, so the record can only ever cause a rebuild, not skip one for a
// different commit. A binary swapped without updating the record fails the
// digest check and is rebuilt.
func (r installRecord) current(want installRecord, installed []byte) bool {
	return r.Commit != "" && r.Commit == want.Commit && r.Package == want.Package &&
		r.SHA256 != "" && r.SHA256 == digest(installed)
}
