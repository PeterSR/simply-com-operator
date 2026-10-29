/*
Copyright 2026 Peter Severin Rasmussen.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"fmt"
	"net/netip"
	"regexp"
	"strings"

	simplycomv1alpha1 "github.com/PeterSR/simply-com-operator/api/v1alpha1"
	"github.com/PeterSR/simply-com-operator/internal/simply"
)

// The operator owns a Simply.com record when the record's comment ends with this marker. Simply.com
// has no labels or metadata on records, and the comment survives every edit made through the API
// or the control panel that does not clear it, so it is the only place ownership can live.
const markerPrefix = "[simply-com-operator"

var (
	markerPattern  = regexp.MustCompile(`\[simply-com-operator owner=(\S+) ref=(\S+)\]$`)
	ownerIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)
	labelPattern   = regexp.MustCompile(`^(\*|[a-z0-9_]([a-z0-9_-]{0,61}[a-z0-9_])?)$`)
)

const (
	typeCNAME = "CNAME"
	typeSRV   = "SRV"
)

// ValidOwnerID reports whether id can be used as an owner id in the ownership marker.
func ValidOwnerID(id string) bool { return ownerIDPattern.MatchString(id) }

func marker(ownerID, ref string) string {
	return fmt.Sprintf("%s owner=%s ref=%s]", markerPrefix, ownerID, ref)
}

// commentWithMarker is the comment the operator writes: the user's own comment, then the marker.
func commentWithMarker(userComment, ownerID, ref string) string {
	if userComment = strings.TrimSpace(userComment); userComment == "" {
		return marker(ownerID, ref)
	}
	return userComment + " " + marker(ownerID, ref)
}

// commentWithoutMarker is comment with the operator's marker taken off, nil when nothing is left.
func commentWithoutMarker(comment *string) *string {
	if comment == nil {
		return nil
	}
	rest := strings.TrimSpace(markerPattern.ReplaceAllString(strings.TrimSpace(*comment), ""))
	if rest == "" {
		return nil
	}
	return &rest
}

// parseMarker returns the owner id and object reference from a record's comment.
func parseMarker(comment *string) (ownerID, ref string, ok bool) {
	if comment == nil {
		return "", "", false
	}
	m := markerPattern.FindStringSubmatch(strings.TrimSpace(*comment))
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// relativeName turns name into the form Simply.com stores: relative to zone, "@" for the apex,
// lower case, no trailing dot.
func relativeName(zone, name string) string {
	zone = strings.TrimSuffix(strings.ToLower(zone), ".")
	name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
	switch {
	case name == "" || name == "@" || name == zone:
		return "@"
	case strings.HasSuffix(name, "."+zone):
		return strings.TrimSuffix(name, "."+zone)
	}
	return name
}

func fqdn(zone, relName string) string {
	zone = strings.TrimSuffix(strings.ToLower(zone), ".")
	if relName == "@" {
		return zone
	}
	return relName + "." + zone
}

func hostname(s string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s)), ".")
}

// normalizeData puts data in a form where two values Simply.com treats as the same compare equal.
func normalizeData(recordType, data string) string {
	data = strings.TrimSpace(data)
	switch recordType {
	case "A", "AAAA":
		if addr, err := netip.ParseAddr(data); err == nil {
			return addr.String()
		}
	case typeCNAME, "MX", "NS", "ALIAS":
		return hostname(data)
	case typeSRV:
		fields := strings.Fields(data)
		if len(fields) > 0 {
			fields[len(fields)-1] = hostname(fields[len(fields)-1])
		}
		return strings.Join(fields, " ")
	case "TXT", "CAA":
		// Simply.com drops double quotes from TXT data; CAA values are compared the same way.
		return strings.ReplaceAll(data, "\"", "")
	case "TLSA", "SSHFP":
		// Hex digests, which may come back in either case.
		return strings.ToLower(strings.Join(strings.Fields(data), " "))
	}
	return data
}

// validateSpec catches what Simply.com would refuse with nothing but "Bad Request".
func validateSpec(spec *simplycomv1alpha1.DNSRecordSpec) error {
	name := relativeName(spec.Zone, spec.Name)
	if name != "@" {
		for label := range strings.SplitSeq(name, ".") {
			if !labelPattern.MatchString(label) {
				return fmt.Errorf("name %q has an invalid label %q", spec.Name, label)
			}
		}
		if name != "*" && strings.Contains(strings.TrimPrefix(name, "*."), "*") {
			return fmt.Errorf("name %q may only have * as its first label", spec.Name)
		}
	}
	data := strings.TrimSpace(spec.Data)
	switch spec.Type {
	case "A":
		if addr, err := netip.ParseAddr(data); err != nil || !addr.Is4() {
			return fmt.Errorf("data %q is not an IPv4 address", spec.Data)
		}
	case "AAAA":
		if addr, err := netip.ParseAddr(data); err != nil || !addr.Is6() || addr.Is4In6() {
			return fmt.Errorf("data %q is not an IPv6 address", spec.Data)
		}
	case typeCNAME:
		if name == "@" {
			return fmt.Errorf("a CNAME cannot be at the zone apex; use ALIAS")
		}
	case "MX", typeSRV:
		if spec.Priority == nil {
			return fmt.Errorf("priority is required for %s records", spec.Type)
		}
	}
	if spec.Type == typeSRV && len(strings.Fields(data)) != 3 {
		return fmt.Errorf("SRV data %q is not \"weight port target\"", spec.Data)
	}
	if strings.Contains(spec.Comment, markerPrefix) {
		return fmt.Errorf("comment may not contain %q", markerPrefix)
	}
	return nil
}

// desiredRecord is the record the spec asks for, comment and marker included.
func desiredRecord(spec *simplycomv1alpha1.DNSRecordSpec, ownerID, ref string) simply.DNSRecord {
	comment := commentWithMarker(spec.Comment, ownerID, ref)
	return simply.DNSRecord{
		Name:     relativeName(spec.Zone, spec.Name),
		Type:     string(spec.Type),
		Data:     strings.TrimSpace(spec.Data),
		TTL:      spec.TTL,
		Priority: spec.Priority,
		Comment:  &comment,
	}
}

func sameRecordData(a, b simply.DNSRecord) bool {
	return strings.EqualFold(a.Name, b.Name) && a.Type == b.Type &&
		normalizeData(a.Type, a.Data) == normalizeData(b.Type, b.Data)
}

// inSync reports whether actual already is what want asks for. A TTL or priority want leaves out
// is not compared.
func inSync(actual, want simply.DNSRecord) bool {
	if !sameRecordData(actual, want) {
		return false
	}
	if want.TTL != nil && (actual.TTL == nil || *actual.TTL != *want.TTL) {
		return false
	}
	if want.Priority != nil && (actual.Priority == nil || *actual.Priority != *want.Priority) {
		return false
	}
	return actual.Comment != nil && want.Comment != nil && *actual.Comment == *want.Comment
}

type planAction int

const (
	actionNone planAction = iota
	actionCreate
	actionUpdate
	actionConflict
)

type plan struct {
	action planAction
	// recordID is the record to update, or the record that is already in sync.
	recordID int64
	// adopting is set when the update takes over an unmanaged record.
	adopting bool
	// duplicates are other records carrying this object's marker; they are deleted.
	duplicates []int64
	conflict   string
	// adoptable is set on a conflict that spec.adopt would resolve.
	adoptable bool
}

// planRecord decides what to do to make zone hold want for the object ref, managed by ownerID.
// It never chooses to change or delete a record that does not carry this object's marker, except
// an exact match when adopt is set.
func planRecord(zone []simply.DNSRecord, want simply.DNSRecord, ownerID, ref string, preferID *int64, adopt bool) plan {
	var owned []simply.DNSRecord
	for _, r := range zone {
		if o, rf, ok := parseMarker(r.Comment); ok && o == ownerID && rf == ref {
			owned = append(owned, r)
		}
	}

	if c := cnameClash(zone, want, ownerID, ref); c != "" {
		return plan{action: actionConflict, conflict: c}
	}

	if len(owned) > 0 {
		keep := pickOwned(owned, want, preferID)
		leftover := false
		for _, r := range owned {
			if preferID == nil && (!strings.EqualFold(r.Name, want.Name) || r.Type != want.Type) {
				leftover = true
				if !adopt {
					// A marker this object never wrote: left behind when an earlier object of the
					// same name was let go without reaching Simply.com. Rewriting or deleting that
					// record is not what anyone asked for.
					return plan{action: actionConflict, adoptable: true, conflict: fmt.Sprintf(
						"record %d (%s %s) carries this object's marker from an earlier object of the same name; "+
							"set spec.adopt to take it over", r.ID, r.Type, r.Name)}
				}
			}
		}
		p := plan{recordID: keep.ID, duplicates: duplicatesOf(owned, keep.ID), action: actionUpdate, adopting: leftover}
		if inSync(keep, want) {
			p.action = actionNone
			return p
		}
		if sameRecordData(keep, want) {
			return p
		}
		// Changing the record must not turn it into a copy of a record this object does not own.
		for _, r := range zone {
			if o, rf, ok := parseMarker(r.Comment); ok && o == ownerID && rf == ref {
				continue
			}
			if sameRecordData(r, want) {
				return plan{action: actionConflict, conflict: fmt.Sprintf(
					"record %d already has this name, type and data", r.ID)}
			}
		}
		return p
	}

	for _, r := range zone {
		if !sameRecordData(r, want) {
			continue
		}
		if o, rf, ok := parseMarker(r.Comment); ok {
			return plan{action: actionConflict, conflict: fmt.Sprintf(
				"record %d with the same name, type and data is managed by owner %q for %s", r.ID, o, rf)}
		}
		if !adopt {
			return plan{action: actionConflict, adoptable: true, conflict: fmt.Sprintf(
				"record %d with the same name, type and data exists and is not managed by the operator; "+
					"set spec.adopt to take it over", r.ID)}
		}
		return plan{action: actionUpdate, recordID: r.ID, adopting: true}
	}
	return plan{action: actionCreate}
}

// cnameClash explains why want cannot coexist with a record this object does not own: a CNAME
// cannot share its name with any other record.
func cnameClash(zone []simply.DNSRecord, want simply.DNSRecord, ownerID, ref string) string {
	for _, r := range zone {
		if !strings.EqualFold(r.Name, want.Name) {
			continue
		}
		if o, rf, ok := parseMarker(r.Comment); ok && o == ownerID && rf == ref {
			continue
		}
		if want.Type == typeCNAME || r.Type == typeCNAME {
			if want.Type == typeCNAME && r.Type == typeCNAME && sameRecordData(r, want) {
				// An identical CNAME is handled as an adoption or ownership conflict.
				continue
			}
			return fmt.Sprintf("%s record %d already exists at %q, and a CNAME cannot share its name with another record",
				r.Type, r.ID, want.Name)
		}
	}
	return ""
}

func pickOwned(owned []simply.DNSRecord, want simply.DNSRecord, preferID *int64) simply.DNSRecord {
	if preferID != nil {
		for _, r := range owned {
			if r.ID == *preferID {
				return r
			}
		}
	}
	for _, r := range owned {
		if inSync(r, want) {
			return r
		}
	}
	return owned[0]
}

func duplicatesOf(owned []simply.DNSRecord, keepID int64) []int64 {
	var ids []int64
	for _, r := range owned {
		if r.ID != keepID {
			ids = append(ids, r.ID)
		}
	}
	return ids
}

// updatePayload is want as it is sent on a PUT over current. A PUT replaces the whole record, so a
// TTL or priority the spec leaves out is carried over from the record instead of being reset.
func updatePayload(current, want simply.DNSRecord) simply.DNSRecord {
	if want.TTL == nil {
		want.TTL = current.TTL
	}
	if want.Priority == nil && current.Type == want.Type {
		want.Priority = current.Priority
	}
	return want
}
