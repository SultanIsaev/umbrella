package syslog

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// nilValue — "-", означает отсутствие значения у поля HEADER (RFC 5424 §6.2).
const nilValue = "-"

// Message — разобранное сообщение RFC 5424.
type Message struct {
	Facility int // 0-23
	Severity int // 0-7
	Version  int

	// Timestamp — нулевое значение (IsZero()), если поле было NILVALUE.
	Timestamp time.Time

	// Hostname/AppName/ProcID/MsgID — "", если поле было NILVALUE.
	Hostname string
	AppName  string
	ProcID   string
	MsgID    string

	StructuredData []SDElement
	Message        string
}

// SDElement — один STRUCTURED-DATA элемент: `[ID PARAM=VALUE ...]`.
type SDElement struct {
	ID     string
	Params []SDParam
}

// SDParam — один параметр внутри SD-ELEMENT.
type SDParam struct {
	Name  string
	Value string
}

// Decode разбирает одно сообщение RFC 5424. Рассчитан на UDP-фрейминг: одна
// датаграмма — одно сообщение, отдельного склеивания/разбиения не требуется
// (в отличие от TCP, где сообщения идут потоком байт без явных границ —
// это отдельная задача, не относящаяся к Decode).
func Decode(data []byte) (Message, error) {
	s := string(data)

	if len(s) == 0 || s[0] != '<' {
		return Message{}, fmt.Errorf("syslog: message must start with '<', got %q", truncate(s, 16))
	}
	end := strings.IndexByte(s, '>')
	if end < 0 {
		return Message{}, fmt.Errorf("syslog: unterminated PRI, missing '>'")
	}
	priVal, err := strconv.Atoi(s[1:end])
	if err != nil || priVal < 0 || priVal > 191 {
		return Message{}, fmt.Errorf("syslog: invalid PRIVAL %q, must be an integer in [0,191]", s[1:end])
	}
	s = s[end+1:]

	var msg Message
	msg.Facility = priVal / 8
	msg.Severity = priVal % 8

	versionTok, s, ok := cutToken(s)
	if !ok {
		return Message{}, fmt.Errorf("syslog: missing VERSION")
	}
	version, err := strconv.Atoi(versionTok)
	if err != nil || version <= 0 {
		return Message{}, fmt.Errorf("syslog: invalid VERSION %q", versionTok)
	}
	msg.Version = version

	tsTok, s, ok := cutToken(s)
	if !ok {
		return Message{}, fmt.Errorf("syslog: missing TIMESTAMP")
	}
	if tsTok != nilValue {
		var ts time.Time
		ts, err = time.Parse(time.RFC3339Nano, tsTok)
		if err != nil {
			return Message{}, fmt.Errorf("syslog: invalid TIMESTAMP %q: %w", tsTok, err)
		}
		msg.Timestamp = ts
	}

	hostTok, s, ok := cutToken(s)
	if !ok {
		return Message{}, fmt.Errorf("syslog: missing HOSTNAME")
	}
	msg.Hostname = valueOrEmpty(hostTok)

	appTok, s, ok := cutToken(s)
	if !ok {
		return Message{}, fmt.Errorf("syslog: missing APP-NAME")
	}
	msg.AppName = valueOrEmpty(appTok)

	procTok, s, ok := cutToken(s)
	if !ok {
		return Message{}, fmt.Errorf("syslog: missing PROCID")
	}
	msg.ProcID = valueOrEmpty(procTok)

	msgIDTok, s, ok := cutToken(s)
	if !ok {
		return Message{}, fmt.Errorf("syslog: missing MSGID")
	}
	msg.MsgID = valueOrEmpty(msgIDTok)

	sd, rest, err := parseStructuredData(s)
	if err != nil {
		return Message{}, fmt.Errorf("syslog: structured data: %w", err)
	}
	msg.StructuredData = sd
	msg.Message = strings.TrimPrefix(rest, " ")

	return msg, nil
}

func valueOrEmpty(tok string) string {
	if tok == nilValue {
		return ""
	}
	return tok
}

// cutToken читает символы до ближайшего пробела (или до конца s). ok=false,
// только если s уже пуст — то есть поле отсутствует вовсе, а не пусто.
func cutToken(s string) (token, rest string, ok bool) {
	if len(s) == 0 {
		return "", "", false
	}
	if i := strings.IndexByte(s, ' '); i >= 0 {
		return s[:i], s[i+1:], true
	}
	return s, "", true
}

// parseStructuredData разбирает STRUCTURED-DATA: NILVALUE "-" или
// последовательность SD-ELEMENT без разделителей между ними.
func parseStructuredData(s string) ([]SDElement, string, error) {
	if strings.HasPrefix(s, nilValue) {
		return nil, s[len(nilValue):], nil
	}

	var elements []SDElement
	for strings.HasPrefix(s, "[") {
		elem, rest, err := parseSDElement(s)
		if err != nil {
			return nil, "", err
		}
		elements = append(elements, elem)
		s = rest
	}
	return elements, s, nil
}

// parseSDElement разбирает один `[ID PARAM="VALUE" ...]`. Значения параметров
// экранируют '"', '\\' и ']' через '\\' (RFC 5424 §6.3.3) — отслеживается
// вручную, посимвольно, с постоянной проверкой границ (вход приходит из
// сети и не заслуживает доверия — см. FuzzDecode).
func parseSDElement(s string) (SDElement, string, error) {
	i := 1 // пропускаем '['

	idStart := i
	for i < len(s) && s[i] != ' ' && s[i] != ']' {
		i++
	}
	if i >= len(s) {
		return SDElement{}, "", fmt.Errorf("unterminated SD-ELEMENT: missing ']'")
	}
	elem := SDElement{ID: s[idStart:i]}

	for i < len(s) && s[i] == ' ' {
		i++ // пропускаем пробел-разделитель

		nameStart := i
		for i < len(s) && s[i] != '=' && s[i] != ']' {
			i++
		}
		if i >= len(s) || s[i] != '=' {
			return SDElement{}, "", fmt.Errorf("SD-ELEMENT %q: expected '=' after param name", elem.ID)
		}
		name := s[nameStart:i]
		i++ // пропускаем '='

		if i >= len(s) || s[i] != '"' {
			return SDElement{}, "", fmt.Errorf("SD-ELEMENT %q: expected opening '\"' for param %q", elem.ID, name)
		}
		i++ // пропускаем открывающую кавычку

		var value strings.Builder
		closed := false
		for i < len(s) {
			c := s[i]
			if c == '\\' && i+1 < len(s) {
				switch s[i+1] {
				case '"', '\\', ']':
					value.WriteByte(s[i+1])
					i += 2
					continue
				}
			}
			if c == '"' {
				closed = true
				i++
				break
			}
			value.WriteByte(c)
			i++
		}
		if !closed {
			return SDElement{}, "", fmt.Errorf("SD-ELEMENT %q: unterminated value for param %q", elem.ID, name)
		}

		elem.Params = append(elem.Params, SDParam{Name: name, Value: value.String()})
	}

	if i >= len(s) || s[i] != ']' {
		return SDElement{}, "", fmt.Errorf("SD-ELEMENT %q: expected ']'", elem.ID)
	}
	i++ // пропускаем ']'

	return elem, s[i:], nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
