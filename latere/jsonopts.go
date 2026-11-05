// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package latere

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
)

// foreignJSON reads JSON this adapter does not produce: an event's payload
// carries what a model and its tools wrote, and a grader's message is model
// output. Such input may repeat a member name, hold invalid UTF-8 or write a
// name in another letter case, and the reader must take it as it comes.
var foreignJSON = json.JoinOptions(
	jsontext.AllowDuplicateNames(true),
	jsontext.AllowInvalidUTF8(true),
	json.MatchCaseInsensitiveNames(true),
)

// sentJSON writes a request body for the platform. The same request is sent
// again under the same idempotency key, so its bytes must not depend on map
// order, and a body can carry user text that is not valid UTF-8 or a raw
// value written by a model. A nil list or map is written as null, as the
// platform has always received it.
var sentJSON = json.JoinOptions(
	json.Deterministic(true),
	json.FormatNilSliceAsNull(true),
	json.FormatNilMapAsNull(true),
	jsontext.AllowDuplicateNames(true),
	jsontext.AllowInvalidUTF8(true),
)

// storedJSON writes bytes that are stored, hashed or compared: they are the
// same for the same value, and are the bytes v1 wrote, with HTML-sensitive
// characters escaped and a nil slice or map written as null.
var storedJSON = json.JoinOptions(
	json.Deterministic(true),
	json.FormatNilSliceAsNull(true),
	json.FormatNilMapAsNull(true),
	jsontext.EscapeForHTML(true),
	jsontext.EscapeForJS(true),
	jsontext.AllowInvalidUTF8(true),
)
