package plugin

import "encoding/json"

type reportLine struct {
	ID        string          `json:"id"`
	Directory string          `json:"directory"`
	Layer     int             `json:"layer"`
	Manifest  json.RawMessage `json:"manifest"`
	Status    int             `json:"status"`
}

func (l reportLine) record() PackageRecord {
	return PackageRecord{ID: l.ID, Directory: l.Directory, Layer: l.Layer, Manifest: append([]byte(nil), l.Manifest...)}
}

func (l reportLine) check() PackageCheck {
	return PackageCheck{ID: l.ID, Directory: l.Directory, Layer: l.Layer, Err: storeStatus(l.Status)}
}

func decodeRecords(raw []byte) ([]PackageRecord, error) {
	var lines []reportLine
	if err := json.Unmarshal(raw, &lines); err != nil {
		return nil, err
	}
	out := make([]PackageRecord, 0, len(lines))
	for _, l := range lines {
		out = append(out, l.record())
	}
	return out, nil
}

func decodeChecks(raw []byte) ([]PackageCheck, error) {
	var lines []reportLine
	if err := json.Unmarshal(raw, &lines); err != nil {
		return nil, err
	}
	out := make([]PackageCheck, 0, len(lines))
	for _, l := range lines {
		out = append(out, l.check())
	}
	return out, nil
}

func decodeUnverified(raw []byte) ([]PackageRecord, []PackageCheck, error) {
	var listing struct {
		Packages []reportLine `json:"packages"`
		Problems []reportLine `json:"problems"`
	}
	if err := json.Unmarshal(raw, &listing); err != nil {
		return nil, nil, err
	}
	records := make([]PackageRecord, 0, len(listing.Packages))
	for _, l := range listing.Packages {
		records = append(records, l.record())
	}
	checks := make([]PackageCheck, 0, len(listing.Problems))
	for _, l := range listing.Problems {
		checks = append(checks, l.check())
	}
	return records, checks, nil
}
