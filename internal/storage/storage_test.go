package storage_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/SultanIsaev/umbrella/internal/storage"
)

func TestFields_MarshalJSON(t *testing.T) {
	fields := storage.Fields{
		{Key: "src_addr", Value: "10.0.0.1"},
		{Key: "src_port", Value: uint16(1234)},
	}

	data, err := json.Marshal(fields)
	require.NoError(t, err)

	// Плоский объект, не массив {"Key":...,"Value":...} — так, будто это
	// был обычный map[string]any. Порядок ключей в JSON-объекте не
	// специфицирован, поэтому сравниваем через unmarshal в map, а не
	// побайтово со строкой.
	var got map[string]any
	require.NoError(t, json.Unmarshal(data, &got))
	require.Equal(t, map[string]any{
		"src_addr": "10.0.0.1",
		"src_port": float64(1234), // JSON-числа всегда float64 при разборе в any
	}, got)
}

func TestFields_MarshalJSON_Empty(t *testing.T) {
	data, err := json.Marshal(storage.Fields(nil))
	require.NoError(t, err)
	require.Equal(t, "{}", string(data))

	data, err = json.Marshal(storage.Fields{})
	require.NoError(t, err)
	require.Equal(t, "{}", string(data))
}

func TestFields_MarshalJSON_Escaping(t *testing.T) {
	fields := storage.Fields{
		{Key: `weird"key`, Value: "line1\nline2 \"quoted\""},
	}

	data, err := json.Marshal(fields)
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(data, &got))
	require.Equal(t, "line1\nline2 \"quoted\"", got[`weird"key`])
}

func TestFields_MarshalJSON_UnsupportedValue(t *testing.T) {
	fields := storage.Fields{
		{Key: "bad", Value: make(chan int)}, // json.Marshal не умеет каналы
	}

	_, err := json.Marshal(fields)
	require.Error(t, err)
}

func TestFields_UnmarshalJSON(t *testing.T) {
	var got storage.Fields
	err := json.Unmarshal([]byte(`{"src_addr":"10.0.0.1","src_port":1234}`), &got)
	require.NoError(t, err)

	require.ElementsMatch(t, storage.Fields{
		{Key: "src_addr", Value: "10.0.0.1"},
		{Key: "src_port", Value: float64(1234)}, // не uint16 — JSON-числа не типизированы
	}, got)
}

func TestFields_MarshalUnmarshal_RoundTrip(t *testing.T) {
	original := storage.Fields{
		{Key: "src_addr", Value: "10.0.0.1"},
		{Key: "dst_addr", Value: "8.8.8.8"},
		{Key: "src_port", Value: uint16(1234)},
		{Key: "dst_port", Value: uint16(443)},
		{Key: "protocol", Value: uint8(6)},
		{Key: "packets", Value: uint32(10)},
		{Key: "bytes", Value: uint32(1500)},
	}

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var got storage.Fields
	require.NoError(t, json.Unmarshal(data, &got))

	// Порядок после round-trip не гарантирован (JSON-объект в Go
	// разбирается через map) — сравниваем как множество пар, не позиционно.
	// Числа возвращаются как float64 — ожидаемое поведение encoding/json
	// для значений, разобранных в any, не специфичное для Fields.
	require.ElementsMatch(t, storage.Fields{
		{Key: "src_addr", Value: "10.0.0.1"},
		{Key: "dst_addr", Value: "8.8.8.8"},
		{Key: "src_port", Value: float64(1234)},
		{Key: "dst_port", Value: float64(443)},
		{Key: "protocol", Value: float64(6)},
		{Key: "packets", Value: float64(10)},
		{Key: "bytes", Value: float64(1500)},
	}, got)
}

func TestEvent_MarshalJSON(t *testing.T) {
	e := storage.Event{
		Timestamp: 1_700_000_000,
		Source:    "netflow5",
		Fields:    storage.Fields{{Key: "src_addr", Value: "10.0.0.1"}},
	}

	data, err := json.Marshal(e)
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(data, &got))
	require.Equal(t, map[string]any{
		"Timestamp": float64(1_700_000_000),
		"Source":    "netflow5",
		"Fields":    map[string]any{"src_addr": "10.0.0.1"},
	}, got)
}
