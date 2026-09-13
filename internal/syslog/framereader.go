package syslog

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
)

// maxMessageSize — верхняя граница на длину одного TCP-сообщения (и на
// длину префикса длины при octet-counting), чтобы отправитель не мог
// вынудить нас выделить неограниченную память, просто не прислав
// разделитель ('\n') или указав огромную заявленную длину. RFC 5424 не
// задаёт жёсткого предела на размер сообщения; 1 МиБ — щедрый запас для
// любого реалистичного syslog-сообщения (тот же принцип явного обоснованного
// лимита, что и bufferSize в internal/ingest).
const maxMessageSize = 1 << 20 // 1 МиБ

type frameMode int

const (
	frameModeUnknown frameMode = iota
	frameModeOctetCounting
	frameModeDelimited
)

// FrameReader реализует разбор границ сообщений RFC 5424 поверх TCP
// (RFC 6587) — то, чего не нужно для UDP (там одна датаграмма уже и есть
// одно сообщение, см. Decode). Возвращает сырые байты одного сообщения за
// раз; сам разбор содержимого — по-прежнему Decode.
//
// Режим фрейминга определяется один раз, по первому байту потока
// (octet-counting начинается с цифры, non-transparent — нет), и дальше не
// меняется: реальные отправители не переключают режим посреди соединения.
type FrameReader struct {
	br   *bufio.Reader
	mode frameMode
}

// NewFrameReader оборачивает r (обычно net.Conn) для последовательного
// чтения отдельных syslog-сообщений.
func NewFrameReader(r io.Reader) *FrameReader {
	return &FrameReader{br: bufio.NewReader(r)}
}

// ReadMessage возвращает следующее сообщение (без обрамления) или io.EOF,
// если поток закончился ровно на границе между сообщениями. Любая другая
// ошибка — реальный обрыв/повреждение фрейминга посреди сообщения.
func (fr *FrameReader) ReadMessage() ([]byte, error) {
	if fr.mode == frameModeUnknown {
		b, err := fr.br.Peek(1)
		if err != nil {
			return nil, err
		}
		if b[0] >= '0' && b[0] <= '9' {
			fr.mode = frameModeOctetCounting
		} else {
			fr.mode = frameModeDelimited
		}
	}

	if fr.mode == frameModeOctetCounting {
		return fr.readOctetCounted()
	}
	return fr.readDelimited()
}

// readOctetCounted читает "<DIGIT+> <ровно N байт сообщения>" (RFC 6587 §3.4.1).
func (fr *FrameReader) readOctetCounted() ([]byte, error) {
	var lengthBuf []byte
	for {
		b, err := fr.br.ReadByte()
		if err != nil {
			if err == io.EOF && len(lengthBuf) == 0 {
				return nil, io.EOF
			}
			return nil, fmt.Errorf("syslog: octet-counting: read length prefix: %w", err)
		}
		if b == ' ' {
			break
		}
		if b < '0' || b > '9' {
			return nil, fmt.Errorf("syslog: octet-counting: invalid byte %q in length prefix", b)
		}
		lengthBuf = append(lengthBuf, b)
		if len(lengthBuf) > 10 { // с запасом больше любого разумного maxMessageSize
			return nil, fmt.Errorf("syslog: octet-counting: length prefix too long")
		}
	}
	if len(lengthBuf) == 0 {
		return nil, fmt.Errorf("syslog: octet-counting: empty length prefix")
	}

	length, err := strconv.Atoi(string(lengthBuf))
	if err != nil || length <= 0 {
		return nil, fmt.Errorf("syslog: octet-counting: invalid length %q", lengthBuf)
	}
	if length > maxMessageSize {
		return nil, fmt.Errorf("syslog: octet-counting: length %d exceeds max %d", length, maxMessageSize)
	}

	buf := make([]byte, length)
	if _, err := io.ReadFull(fr.br, buf); err != nil {
		return nil, fmt.Errorf("syslog: octet-counting: read message body: %w", err)
	}
	return buf, nil
}

// readDelimited читает сообщение до '\n' (не включая его), с опциональным
// завершающим '\r' (CRLF). Читаем побайтово через ReadByte, а не
// bufio.Reader.ReadBytes — у последнего нет верхней границы на рост
// внутреннего буфера, если разделитель так и не встретится.
func (fr *FrameReader) readDelimited() ([]byte, error) {
	var line []byte
	for {
		b, err := fr.br.ReadByte()
		if err != nil {
			if err == io.EOF && len(line) > 0 {
				return trimCR(line), nil
			}
			return nil, err
		}
		if b == '\n' {
			return trimCR(line), nil
		}
		line = append(line, b)
		if len(line) > maxMessageSize {
			return nil, fmt.Errorf("syslog: delimited: message exceeds max size %d bytes", maxMessageSize)
		}
	}
}

func trimCR(b []byte) []byte {
	if len(b) > 0 && b[len(b)-1] == '\r' {
		return b[:len(b)-1]
	}
	return b
}
