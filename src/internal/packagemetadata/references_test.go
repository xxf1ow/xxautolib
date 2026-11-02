package packagemetadata

import "testing"

func TestTransportedProducerReferences(t *testing.T) {
	for _, prefix := range []string{`C:\producer space\install`, `C:/producer space/install`} {
		for _, tc := range []struct {
			text  string
			valid bool
		}{
			{`set(path "C:/producer space/install/queue/1.0/include")`, true},
			{`set(path "C:\producer space\install/queue/1.0/include")`, true},
			{`set(path "C:/producer space/install/other/2/include")`, false},
			{`set(path "C:\producer space\install/other/2/include")`, false},
		} {
			if err := CheckProducerReferences(tc.text, prefix, []string{"queue/1.0"}); (err == nil) != tc.valid {
				t.Errorf("prefix %q text %q: %v", prefix, tc.text, err)
			}
		}
	}
}
