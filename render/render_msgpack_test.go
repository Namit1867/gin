// Copyright 2014 Manu Martinez-Almeida. All rights reserved.
// Use of this source code is governed by a MIT style
// license that can be found in the LICENSE file.

//go:build !nomsgpack

package render

import (
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ugorji/go/codec"
)

func TestRenderMsgPack(t *testing.T) {
	w := httptest.NewRecorder()
	data := map[string]any{
		"foo": "bar",
	}

	(MsgPack{data}).WriteContentType(w)
	assert.Equal(t, "application/msgpack; charset=utf-8", w.Header().Get("Content-Type"))

	err := (MsgPack{data}).Render(w)

	require.NoError(t, err)

	var decoded map[string]any
	var mh codec.MsgpackHandle
	mh.RawToString = true
	err = codec.NewDecoderBytes(w.Body.Bytes(), &mh).Decode(&decoded)
	require.NoError(t, err)
	assert.Equal(t, data, decoded)
	assert.Equal(t, "application/msgpack; charset=utf-8", w.Header().Get("Content-Type"))
}

func TestWriteMsgPack(t *testing.T) {
	w := httptest.NewRecorder()
	data := map[string]any{
		"foo": "bar",
		"num": 42,
	}

	err := WriteMsgPack(w, data)
	require.NoError(t, err)

	assert.Equal(t, "application/msgpack; charset=utf-8", w.Header().Get("Content-Type"))