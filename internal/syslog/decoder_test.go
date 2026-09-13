package syslog

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDecode_Valid(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want Message
	}{
		{
			// Пример почти дословно из RFC 5424 §6.5.
			name: "RFC 5424 пример со structured data",
			in:   `<34>1 2003-10-11T22:14:15.003Z mymachine.example.com su - ID47 [exampleSDID@32473 iut="3" eventSource="Application" eventID="1011"] BOM'su root' failed for lonvick on /dev/pts/8`,
			want: Message{
				Facility:  4,
				Severity:  2,
				Version:   1,
				Timestamp: mustParseTime(t, "2003-10-11T22:14:15.003Z"),
				Hostname:  "mymachine.example.com",
				AppName:   "su",
				ProcID:    "",
				MsgID:     "ID47",
				StructuredData: []SDElement{
					{
						ID: "exampleSDID@32473",
						Params: []SDParam{
							{Name: "iut", Value: "3"},
							{Name: "eventSource", Value: "Application"},
							{Name: "eventID", Value: "1011"},
						},
					},
				},
				Message: `BOM'su root' failed for lonvick on /dev/pts/8`,
			},
		},
		{
			name: "все NILVALUE, без MSG",
			in:   `<13>1 - - - - -`,
			want: Message{
				Facility: 1,
				Severity: 5,
				Version:  1,
			},
		},
		{
			name: "два SD-ELEMENT подряд",
			in:   `<165>1 2003-08-24T05:14:15.000003-07:00 host - - - [a x="1"][b y="2"] hi`,
			want: Message{
				Facility:  20,
				Severity:  5,
				Version:   1,
				Timestamp: mustParseTime(t, "2003-08-24T05:14:15.000003-07:00"),
				Hostname:  "host",
				StructuredData: []SDElement{
					{ID: "a", Params: []SDParam{{Name: "x", Value: "1"}}},
					{ID: "b", Params: []SDParam{{Name: "y", Value: "2"}}},
				},
				Message: "hi",
			},
		},
		{
			name: "экранированные кавычка, бэкслеш и ']' в значении параметра",
			in:   `<1>1 - - - - - [id x="a\"b\\c\]d"] msg`,
			want: Message{
				Facility: 0,
				Severity: 1,
				Version:  1,
				StructuredData: []SDElement{
					{ID: "id", Params: []SDParam{{Name: "x", Value: `a"b\c]d`}}},
				},
				Message: "msg",
			},
		},
		{
			name: "structured data без MSG",
			in:   `<1>1 - - - - - [id x="1"]`,
			want: Message{
				Facility:       0,
				Severity:       1,
				Version:        1,
				StructuredData: []SDElement{{ID: "id", Params: []SDParam{{Name: "x", Value: "1"}}}},
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
		{name: "нет PRI", in: "not a syslog message"},
		{name: "PRI не закрыт", in: "<34"},
		{name: "PRIVAL не число", in: "<abc>1 - - - - -"},
		{name: "PRIVAL вне диапазона", in: "<192>1 - - - - -"},
		{name: "нет VERSION", in: "<34>"},
		{name: "VERSION не число", in: "<34>x - - - - -"},
		{name: "нет TIMESTAMP", in: "<34>1"},
		{name: "битый TIMESTAMP", in: "<34>1 not-a-timestamp host - - -"},
		{name: "нет HOSTNAME", in: "<34>1 -"},
		{name: "SD-ELEMENT без ']'", in: `<34>1 - - - - - [id x="1"`},
		{name: "SD-ELEMENT без '=' у параметра", in: `<34>1 - - - - - [id x"1"]`},
		{name: "SD-ELEMENT без открывающей кавычки", in: `<34>1 - - - - - [id x=1]`},
		{name: "SD-ELEMENT с незакрытой кавычкой", in: `<34>1 - - - - - [id x="1]`},
		{name: "SD-ELEMENT без ID и без ']'", in: `<34>1 - - - - - [`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Decode([]byte(tt.in))
			require.Error(t, err)
		})
	}
}

func mustParseTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339Nano, s)
	require.NoError(t, err)
	return ts
}

// FuzzDecode — DoD M6: корпус засеян валидными и намеренно битыми
// сообщениями, Decode не должен паниковать ни на каком входе. Единственное
// требуемое свойство — отсутствие паники/зависания, поэтому f.Fuzz ничего
// не проверяет по возвращаемому значению, кроме самого факта, что вызов
// завершился.
func FuzzDecode(f *testing.F) {
	seeds := []string{
		`<34>1 2003-10-11T22:14:15.003Z mymachine.example.com su - ID47 [exampleSDID@32473 iut="3" eventSource="Application" eventID="1011"] BOM'su root' failed for lonvick on /dev/pts/8`,
		`<13>1 - - - - -`,
		`<165>1 2003-08-24T05:14:15.000003-07:00 host - - - [a x="1"][b y="2"] hi`,
		`<1>1 - - - - - [id x="a\"b\\c\]d"] msg`,
		"",
		"not a syslog message",
		"<34",
		"<192>1 - - - - -",
		`<34>1 - - - - - [id x="1"`,
		`<34>1 - - - - - [`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = Decode(data)
	})
}
