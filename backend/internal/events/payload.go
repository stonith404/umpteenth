package events

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"unicode/utf8"
)

// shortenPayload fits an oversized payload under maxPayloadBytes, marking it as truncated and pointing at the blob that holds it whole
// Readers look up fields by name, so the payload stays an object with the same keys rather than becoming a preview of its encoding
func shortenPayload(payload []byte, blobKey string) []byte {
	keys, values, err := objectEntries(payload)
	if err != nil {
		return fitJSON(payload, maxPayloadBytes)
	}
	keys, values = append(keys, "truncated"), append(values, json.RawMessage("true"))
	if blobKey != "" {
		encoded, _ := json.Marshal(blobKey)
		keys, values = append(keys, "blobKey"), append(values, encoded)
	}
	return fitObject(keys, values, maxPayloadBytes)
}

// fitJSON shortens an encoded value to at most budget bytes where it can
// Long strings lose their middle, lists their last items and objects the entries that don't fit, while numbers and booleans are never cut
func fitJSON(raw json.RawMessage, budget int) json.RawMessage {
	if len(raw) <= budget {
		return raw
	}
	switch raw[0] {
	case '"':
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return raw
		}
		return fitString(s, budget)
	case '[':
		var items []json.RawMessage
		if json.Unmarshal(raw, &items) != nil {
			return raw
		}

		// A list keeps its first items whole, and only a first item that alone is too long is cut, since cutting every item would leave none readable
		var b bytes.Buffer
		b.WriteByte('[')
		for _, item := range items {
			// The room left leaves space for a comma and the closing bracket
			room := budget - b.Len() - 2
			if b.Len() == 1 {
				item = fitJSON(item, room)
			}
			if len(item) > room {
				break
			}
			if b.Len() > 1 {
				b.WriteByte(',')
			}
			b.Write(item)
		}
		b.WriteByte(']')
		return b.Bytes()
	case '{':
		keys, values, err := objectEntries(raw)
		if err != nil {
			return raw
		}
		return fitObject(keys, values, budget)
	}
	return raw
}

// fitObject encodes an object's entries in their original order in at most budget bytes
// The smallest values are placed first and kept whole, and the larger ones share the room they leave, so flags, IDs and costs survive next to shortened text
func fitObject(keys []string, values []json.RawMessage, budget int) json.RawMessage {
	order := make([]int, len(keys))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int { return cmp.Compare(len(values[a]), len(values[b])) })

	// Each entry gets an equal share of the room the smaller entries left, and one that can't fit even shortened is left out
	encodedKeys := make([][]byte, len(keys))
	fitted := make([]json.RawMessage, len(values))
	remaining := budget - 2
	for n, i := range order {
		encodedKeys[i], _ = json.Marshal(keys[i])
		// The key's colon and comma come on top of the key itself
		overhead := len(encodedKeys[i]) + 2
		value := fitJSON(values[i], remaining/(len(order)-n)-overhead)
		if overhead+len(value) > remaining {
			continue
		}
		fitted[i] = value
		remaining -= overhead + len(value)
	}

	// The entries are written in their original order, since readers such as the reflection transcript clip the start of a payload
	var b bytes.Buffer
	b.WriteByte('{')
	for i, value := range fitted {
		if value == nil {
			continue
		}
		if b.Len() > 1 {
			b.WriteByte(',')
		}
		b.Write(encodedKeys[i])
		b.WriteByte(':')
		b.Write(value)
	}
	b.WriteByte('}')
	return b.Bytes()
}

// fitString cuts the middle out of s until its encoding fits in budget bytes, keeping the start and end where commands print their exit code and last errors
func fitString(s string, budget int) json.RawMessage {
	// Escaping never makes a string shorter, so the search starts from keeping budget bytes and scales down by how much the encoding overshoots
	for keep := min(len(s), budget); keep > 0; {
		out, _ := json.Marshal(cutMiddle(s, keep))
		if len(out) <= budget {
			return out
		}
		keep = min(keep*budget/len(out), keep-1)
	}
	return json.RawMessage(`""`)
}

// cutMiddle keeps about keep bytes of s, split between its start and end on rune boundaries, with a note of how much was left out
func cutMiddle(s string, keep int) string {
	end, start := keep/2, len(s)-(keep-keep/2)
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	return s[:end] + fmt.Sprintf("\n\n[… %d characters omitted …]\n\n", start-end) + s[start:]
}

// objectEntries decodes the entries of an encoded object in their order, which decoding into a map would lose
func objectEntries(raw json.RawMessage) ([]string, []json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, nil, errors.New("not an object")
	}
	var (
		keys   []string
		values []json.RawMessage
	)
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, nil, err
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, nil, err
		}
		keys = append(keys, tok.(string))
		values = append(values, value)
	}
	return keys, values, nil
}
