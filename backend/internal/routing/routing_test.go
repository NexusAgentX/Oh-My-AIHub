package routing

import "testing"

func TestValidateNormalizesAndRejectsBadPreferences(t *testing.T) {
	id := "0b7a8ad4-76f3-4a6c-8b8c-0d2d8a7b6a11"
	attempts, timeout := int32(3), int32(5000)
	pref, err := Validate(Pref{ModelID: "m", Mode: ModeManual, Order: []string{id, id}, Excluded: []string{id}, MaxAttempts: &attempts, TTFTTimeoutMS: &timeout})
	if err != nil || len(pref.Order) != 1 || len(pref.Excluded) != 1 {
		t.Fatalf("pref = %+v, %v", pref, err)
	}
	tooMany, tooFast := int32(11), int32(10)
	for _, bad := range []Pref{
		{Mode: "random"}, {Mode: ModeManual, Order: []string{"not-a-uuid"}}, {Mode: ModeCheapest, MaxAttempts: &tooMany}, {Mode: ModeCheapest, TTFTTimeoutMS: &tooFast},
	} {
		if _, err := Validate(bad); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	if Default("m").Mode != ModeCheapest || Default("m").Source != SourceDefault {
		t.Fatal("default preference must be cheapest-first")
	}
}
