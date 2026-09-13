package cef

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDecode_Valid(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want Message
	}{
		{
			name: "типичное сообщение с несколькими полями extension",
			in:   `CEF:0|Security|threatmanager|1.0|100|worm successfully stopped|10|src=10.0.0.1 dst=2.1.2.2 spt=1232`,
			want: Message{
				Version:            0,
				DeviceVendor:       "Security",
				DeviceProduct:      "threatmanager",
				DeviceVersion:      "1.0",
				DeviceEventClassID: "100",
				Name:               "worm successfully stopped",
				Severity:           "10",
				Extension: map[string]string{
					"src": "10.0.0.1",
					"dst": "2.1.2.2",
					"spt": "1232",
				},
			},
		},
		{
			name: "строковый Severity",
			in:   `CEF:0|Vendor|Product|1.0|100|Name|Low|act=blocked`,
			want: Message{
				Version:            0,
				DeviceVendor:       "Vendor",
				DeviceProduct:      "Product",
				DeviceVersion:      "1.0",
				DeviceEventClassID: "100",
				Name:               "Name",
				Severity:           "Low",
				Extension:          map[string]string{"act": "blocked"},
			},
		},
		{
			name: "значение extension со встроенными пробелами",
			in:   `CEF:0|V|P|1.0|100|N|5|msg=user logged in from remote host act=allow`,
			want: Message{
				Version:            0,
				DeviceVendor:       "V",
				DeviceProduct:      "P",
				DeviceVersion:      "1.0",
				DeviceEventClassID: "100",
				Name:               "N",
				Severity:           "5",
				Extension: map[string]string{
					"msg": "user logged in from remote host",
					"act": "allow",
				},
			},
		},
		{
			name: "экранированные '|' и '\\\\' в заголовке, '\\\\'/'\\\\=' в значении",
			in:   `CEF:0|Vendor\|Sub|Pro\\duct|1.0|100|N|5|msg=a\=b\\c`,
			want: Message{
				Version:            0,
				DeviceVendor:       `Vendor|Sub`,
				DeviceProduct:      `Pro\duct`,
				DeviceVersion:      "1.0",
				DeviceEventClassID: "100",
				Name:               "N",
				Severity:           "5",
				Extension:          map[string]string{"msg": `a=b\c`},
			},
		},
		{
			name: "без extension",
			in:   `CEF:0|V|P|1.0|100|N|5|`,
			want: Message{
				Version:            0,
				DeviceVendor:       "V",
				DeviceProduct:      "P",
				DeviceVersion:      "1.0",
				DeviceEventClassID: "100",
				Name:               "N",
				Severity:           "5",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Decode([]byte(tt.in))
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestDecode_Malformed(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{name: "пустое сообщение", in: ""},
		{name: "нет префикса CEF:", in: "not a cef message"},
		{name: "не хватает полей заголовка", in: "CEF:0|V|P|1.0"},
		{name: "Version не число", in: "CEF:x|V|P|1.0|100|N|5|"},
		{name: "ключ extension без '='", in: "CEF:0|V|P|1.0|100|N|5|src"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Decode([]byte(tt.in))
			require.Error(t, err)
		})
	}
}

// FuzzDecode — DoD M6, тот же принцип, что и syslog.FuzzDecode: только
// отсутствие паники/зависания, без проверки семантики результата.
func FuzzDecode(f *testing.F) {
	seeds := []string{
		`CEF:0|Security|threatmanager|1.0|100|worm successfully stopped|10|src=10.0.0.1 dst=2.1.2.2 spt=1232`,
		`CEF:0|V|P|1.0|100|N|5|`,
		`CEF:0|Vendor\|Sub|Pro\\duct|1.0|100|N|5|msg=a\=b\\c`,
		`CEF:0|V|P|1.0|100|N|5|msg=user logged in from remote host act=allow`,
		"",
		"not a cef message",
		"CEF:0|V|P|1.0",
		"CEF:0|V|P|1.0|100|N|5|src",
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = Decode(data)
	})
}
