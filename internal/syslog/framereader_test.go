package syslog

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func readAllMessages(t *testing.T, r io.Reader) [][]byte {
	t.Helper()
	fr := NewFrameReader(r)
	var out [][]byte
	for {
		msg, err := fr.ReadMessage()
		if err == io.EOF {
			return out
		}
		require.NoError(t, err)
		out = append(out, msg)
	}
}

func TestFrameReader_OctetCounting(t *testing.T) {
	in := "5 hello7 world!!"
	got := readAllMessages(t, strings.NewReader(in))
	require.Equal(t, [][]byte{[]byte("hello"), []byte("world!!")}, got)
}

func TestFrameReader_Delimited(t *testing.T) {
	in := "hello\nworld\n"
	got := readAllMessages(t, strings.NewReader(in))
	require.Equal(t, [][]byte{[]byte("hello"), []byte("world")}, got)
}

func TestFrameReader_Delimited_CRLF(t *testing.T) {
	in := "hello\r\nworld\r\n"
	got := readAllMessages(t, strings.NewReader(in))
	require.Equal(t, [][]byte{[]byte("hello"), []byte("world")}, got)
}

func TestFrameReader_Delimited_NoTrailingNewline(t *testing.T) {
	in := "hello\nworld"
	got := readAllMessages(t, strings.NewReader(in))
	require.Equal(t, [][]byte{[]byte("hello"), []byte("world")}, got)
}

func TestFrameReader_ModeDeterminedOnce(t *testing.T) {
	// Первое сообщение начинается с цифры -> octet-counting на всё
	// соединение, даже если тело следующего сообщения тоже начинается с
	// цифры, которую можно было бы принять за новый префикс длины.
	in := "1 a1 5"
	got := readAllMessages(t, strings.NewReader(in))
	require.Equal(t, [][]byte{[]byte("a"), []byte("5")}, got)
}

func TestFrameReader_EmptyStream(t *testing.T) {
	got := readAllMessages(t, strings.NewReader(""))
	require.Empty(t, got)
}

func TestFrameReader_WithDecode(t *testing.T) {
	msg1 := `<34>1 2003-10-11T22:14:15.003Z host su - ID47 - hello`
	msg2 := `<13>1 - - - - - world`
	in := strings.Join([]string{msg1, msg2}, "\n") + "\n"

	fr := NewFrameReader(strings.NewReader(in))

	raw1, err := fr.ReadMessage()
	require.NoError(t, err)
	decoded1, err := Decode(raw1)
	require.NoError(t, err)
	require.Equal(t, "hello", decoded1.Message)

	raw2, err := fr.ReadMessage()
	require.NoError(t, err)
	decoded2, err := Decode(raw2)
	require.NoError(t, err)
	require.Equal(t, "world", decoded2.Message)

	_, err = fr.ReadMessage()
	require.ErrorIs(t, err, io.EOF)
}

func TestFrameReader_Malformed(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{name: "octet-counting: нецифровой байт в префиксе длины", in: "1a2 xx"},
		{name: "octet-counting: тело короче заявленной длины", in: "10 short"},
		{name: "octet-counting: нулевая длина", in: "0 "},
		{name: "octet-counting: слишком длинный префикс длины", in: "12345678901 x"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fr := NewFrameReader(strings.NewReader(tt.in))
			_, err := fr.ReadMessage()
			require.Error(t, err)
		})
	}
}

func TestFrameReader_DelimitedExceedsMaxSize(t *testing.T) {
	oversized := bytes.Repeat([]byte("a"), maxMessageSize+10)
	fr := NewFrameReader(bytes.NewReader(oversized)) // без '\n' вообще
	_, err := fr.ReadMessage()
	require.Error(t, err)
}

func TestFrameReader_OctetCountingExceedsMaxSize(t *testing.T) {
	in := "99999999 x" // заявленная длина больше maxMessageSize
	fr := NewFrameReader(strings.NewReader(in))
	_, err := fr.ReadMessage()
	require.Error(t, err)
}

// FuzzFrameReader — DoD M6: ReadMessage не должен паниковать/зависать ни на
// каком байтовом потоке. Каждый успешный ReadMessage гарантированно
// продвигает чтение хотя бы на 1 байт (см. readOctetCounted/readDelimited),
// поэтому цикл до io.EOF/ошибки для конечного входа тоже гарантированно
// завершается — бесконечный цикл фаззеру не грозит.
func FuzzFrameReader(f *testing.F) {
	seeds := []string{
		"5 hello7 world!!",
		"hello\nworld\n",
		"hello\r\nworld\r\n",
		"",
		"1a2 xx",
		"10 short",
		"0 ",
		"not digits but no newline either",
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		fr := NewFrameReader(bytes.NewReader(data))
		for i := 0; i < 10_000; i++ {
			_, err := fr.ReadMessage()
			if err != nil {
				return
			}
		}
	})
}
