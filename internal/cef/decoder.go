package cef

import (
	"fmt"
	"strconv"
	"strings"
)

// prefix — CEF-сообщения всегда начинаются с "CEF:", за которым сразу без
// разделителя идёт Version.
const prefix = "CEF:"

// headerFieldCount — число pipe-разделённых полей заголовка ПОСЛЕ Version:
// DeviceVendor|DeviceProduct|DeviceVersion|DeviceEventClassID|Name|Severity
// — 6 штук, плюс сам Version перед ними = 7 полей до Extension.
const headerFieldCount = 7

// Message — разобранное сообщение Common Event Format. Обычно приезжает
// внутри MSG-части уже разобранного syslog.Message, а не напрямую с сети.
type Message struct {
	Version            int
	DeviceVendor       string
	DeviceProduct      string
	DeviceVersion      string
	DeviceEventClassID string
	Name               string
	// Severity — по спецификации либо целое 0-10, либо одно из
	// Unknown/Low/Medium/High/Very-High — оставлен строкой, не
	// разбирается принудительно в int.
	Severity string

	Extension map[string]string
}

// Decode разбирает одно сообщение CEF: `CEF:Version|Vendor|...|Severity|Extension`.
func Decode(data []byte) (Message, error) {
	s := string(data)
	if !strings.HasPrefix(s, prefix) {
		return Message{}, fmt.Errorf("cef: message must start with %q", prefix)
	}
	s = s[len(prefix):]

	header, extRaw, err := splitPipeFields(s, headerFieldCount)
	if err != nil {
		return Message{}, fmt.Errorf("cef: header: %w", err)
	}

	version, err := strconv.Atoi(header[0])
	if err != nil {
		return Message{}, fmt.Errorf("cef: invalid Version %q: %w", header[0], err)
	}

	ext, err := parseExtension(extRaw)
	if err != nil {
		return Message{}, fmt.Errorf("cef: extension: %w", err)
	}

	return Message{
		Version:            version,
		DeviceVendor:       header[1],
		DeviceProduct:      header[2],
		DeviceVersion:      header[3],
		DeviceEventClassID: header[4],
		Name:               header[5],
		Severity:           header[6],
		Extension:          ext,
	}, nil
}

// splitPipeFields читает ровно n полей, разделённых непроэкранированным '|'
// (заголовок CEF), и возвращает их вместе с остатком строки после n-го '|'
// (это Extension — там '|' уже не разделитель, поэтому дальше не режем).
// '\|' и '\\' внутри поля — экранированные литералы, не разделитель.
func splitPipeFields(s string, n int) ([]string, string, error) {
	fields := make([]string, 0, n)
	var cur strings.Builder

	i := 0
	for i < len(s) && len(fields) < n {
		c := s[i]
		if c == '\\' && i+1 < len(s) && (s[i+1] == '|' || s[i+1] == '\\') {
			cur.WriteByte(s[i+1])
			i += 2
			continue
		}
		if c == '|' {
			fields = append(fields, cur.String())
			cur.Reset()
			i++
			continue
		}
		cur.WriteByte(c)
		i++
	}

	if len(fields) < n {
		return nil, "", fmt.Errorf("expected %d pipe-delimited field(s), got %d", n, len(fields))
	}
	return fields, s[i:], nil
}

// parseExtension разбирает "key1=value1 key2=value2 ...". Значения могут
// содержать пробелы — граница между значением текущего ключа и следующим
// ключом ищется как непроэкранированный пробел, за которым следует токен без
// пробелов, оканчивающийся на '=' (эвристика, общепринятая для CEF: имена
// ключей не содержат пробелов, поэтому "следующий токен похож на ключ" —
// надёжный признак конца значения). В значениях экранируются '\\', '\=',
// '\n', '\r'.
func parseExtension(s string) (map[string]string, error) {
	if len(s) == 0 {
		return nil, nil
	}

	fields := make(map[string]string)
	i := 0
	n := len(s)

	for i < n {
		for i < n && s[i] == ' ' {
			i++
		}
		if i >= n {
			break
		}

		keyStart := i
		for i < n && s[i] != '=' && s[i] != ' ' {
			i++
		}
		if i >= n || s[i] != '=' {
			return nil, fmt.Errorf("key %q not followed by '='", s[keyStart:i])
		}
		key := s[keyStart:i]
		i++ // пропускаем '='

		valueStart := i
		valueEnd := n
		j := i
		for j < n {
			if s[j] == '\\' && j+1 < n {
				j += 2
				continue
			}
			if s[j] == ' ' {
				k := j + 1
				tokStart := k
				for k < n && s[k] != '=' && s[k] != ' ' {
					k++
				}
				if k < n && s[k] == '=' && k > tokStart {
					valueEnd = j
					break
				}
			}
			j++
		}

		fields[key] = unescapeValue(s[valueStart:valueEnd])
		i = valueEnd
	}

	return fields, nil
}

func unescapeValue(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case '=', '\\':
				b.WriteByte(s[i+1])
				i++
				continue
			case 'n':
				b.WriteByte('\n')
				i++
				continue
			case 'r':
				b.WriteByte('\r')
				i++
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
