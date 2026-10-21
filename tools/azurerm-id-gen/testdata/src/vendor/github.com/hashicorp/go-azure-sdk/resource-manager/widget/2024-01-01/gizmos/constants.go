package gizmos

// RecordType is the record id's constant segment (#2140): the generator
// reads its possible values from PossibleValuesForRecordType.
type RecordType string

const (
	RecordTypeA     RecordType = "A"
	RecordTypeCNAME RecordType = "CNAME"
)

func PossibleValuesForRecordType() []string {
	return []string{
		string(RecordTypeA),
		string(RecordTypeCNAME),
		"TXT",
	}
}
