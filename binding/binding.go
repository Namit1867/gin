// Copyright 2014 Manu Martinez-Almeida. All rights reserved.
// Use of this source code is governed by a MIT style
// license that can be found in the LICENSE file.

//go:build !nomsgpack

package binding

import "net/http"

// Content-Type MIME of the most common data formats.
const (
	MIMEJSON              = "application/json"
	MIMEHTML              = "text/html"
	MIMEXML               = "application/xml"
	MIMEXML2              = "text/xml"
	MIMEPlain             = "text/plain"
	MIMEPOSTForm          = "application/x-www-form-urlencoded"
	MIMEMultipartPOSTForm = "multipart/form-data"
	MIMEPROTOBUF          = "application/x-protobuf"
	MIMEMSGPACK           = "application/x-msgpack"
	MIMEMSGPACK2          = "application/msgpack"
	MIMEYAML              = "application/x-yaml"
	MIMEYAML2             = "application/yaml"
	MIMETOML              = "application/toml"
	MIMEBSON              = "application/bson"
)

// Binding describes the interface which needs to be implemented for binding the
// data present in the request such as JSON request body, query parameters or
// the form POST.
type Binding interface {
	Name() string
	Bind(*http.Request, any) error
}

// BindingBody adds BindBody method to Binding. BindBody is similar with Bind,
// but it reads the body from supplied bytes instead of req.Body.
type BindingBody interface {
	Binding
	BindBody([]byte, any) error
}

// BindingUri adds BindUri method to Binding. BindUri is similar with Bind,
// but it reads the Params.
type BindingUri interface {
	Name() string
	BindUri(map[string][]string, any) error
}

// StructValidator is the minimal interface which needs to be implemented in
// order for it to be used as the validator engine for ensuring the correctness
// of the request. Gin provides a default implementation for this using
// https://github.com/go-playground/validator/tree/v10.6.1.
type StructValidator interface {
	// ValidateStruct can receive any kind of type and it should never panic, even if the configuration is not right.
	// If the received type is a slice|array, the validation should be performed travel on every element.
	// If the received type is not a struct or slice|array, any validation should be skipped and nil must be returned.
	// If the received type is a struct or pointer to a struct, the validation should be performed.
	// If the struct is not valid or the validation itself fails, a descriptive error should be returned.
	// Otherwise nil must be returned.
	ValidateStruct(any) error

	// Engine returns the underlying validator engine which powers the
	// StructValidator implementation.
	Engine() any
}

// Validator is the default validator which implements the StructValidator
// interface. It uses https://github.com/go-playground/validator/tree/v10.6.1
// under the hood.
var Validator StructValidator = &defaultValidator{}

// These implement the Binding interface and can be used to bind the data
// present in the request to struct instances.
var (
	JSON          BindingBody = jsonBinding{}
	XML           BindingBody = xmlBinding{}
	Form          Binding     = formBinding{}
	Query         Binding     = queryBinding{}
	FormPost      Binding     = formPostBinding{}
	FormMultipart Binding     = formMultipartBinding{}
	ProtoBuf      BindingBody = protobufBinding{}
	MsgPack       BindingBody = msgpackBinding{}
	YAML          BindingBody = yamlBinding{}
	Uri           BindingUri  = uriBinding{}
	Header        Binding     = headerBinding{}
	Plain         BindingBody = plainBinding{}
	TOML          BindingBody = tomlBinding{}
	BSON          BindingBody = bsonBinding{}
)

// Default returns the appropriate Binding instance based on the HTTP method
// and the content type.
func Default(method, contentType string) Binding {
	if method == http.MethodGet {
		return Form
	}

	switch contentType {
	case MIMEJSON:
		return JSON
	case MIMEXML, MIMEXML2:
		return XML
	case MIMEPROTOBUF:
		return ProtoBuf
	case MIMEMSGPACK, MIMEMSGPACK2:
		return MsgPack
	case MIMEYAML, MIMEYAML2:
		return YAML
	case MIMETOML:
		return TOML
	case MIMEMultipartPOSTForm:
		return FormMultipart
	case MIMEBSON:
		return BSON
	default: // case MIMEPOSTForm:
		return Form
	}
}

func validate(obj any) error {
	if Validator == nil {
		return nil
	}
	return Validator.ValidateStruct(obj)
}



type bindTestData struct {
	need any
	got  any
	in   map[string][]string
}

func TestMappingTimeUnixNano(t *testing.T) {
	type needFixUnixNanoEmpty struct {
		CreateTime time.Time `form:"createTime" time_format:"unixNano"`
	}

	// ok
	tests := []bindTestData{
		{need: &needFixUnixNanoEmpty{}, got: &needFixUnixNanoEmpty{}, in: formSource{"createTime": []string{"   "}}},
		{need: &needFixUnixNanoEmpty{}, got: &needFixUnixNanoEmpty{}, in: formSource{"createTime": []string{}}},
	}

	for _, v := range tests {
		err := mapForm(v.got, v.in)
		require.NoError(t, err)
		assert.Equal(t, v.need, v.got)
	}
}

func TestMappingTimeDuration(t *testing.T) {
	type needFixDurationEmpty struct {
		Duration time.Duration `form:"duration"`
	}

	var s struct {
		D time.Duration
	}

	// ok
	err := mappingByPtr(&s, formSource{"D": {"5s"}}, "form")
	require.NoError(t, err)
	assert.Equal(t, 5*time.Second, s.D)

	// ok
	tests := []bindTestData{
		{need: &needFixDurationEmpty{}, got: &needFixDurationEmpty{}, in: formSource{"duration": []string{"   "}}},
		{need: &needFixDurationEmpty{}, got: &needFixDurationEmpty{}, in: formSource{"duration": []string{}}},
	}

	for _, v := range tests {
		err := mapForm(v.got, v.in)
		require.NoError(t, err)
		assert.Equal(t, v.need, v.got)
	}
	// error
	err = mappingByPtr(&s, formSource{"D": {"wrong"}}, "form")
	require.Error(t, err)
}

func TestMappingSlice(t *testing.T) {
	var s struct {
		Slice []int `form:"slice,default=9"`
	}

	// default value
	err := mappingByPtr(&s, formSource{}, "form")
	require.NoError(t, err)
	assert.Equal(t, []int{9}, s.Slice)

	// ok
	err = mappingByPtr(&s, formSource{"slice": {"3", "4"}}, "form")
	require.NoError(t, err)
	assert.Equal(t, []int{3, 4}, s.Slice)

	// error
	err = mappingByPtr(&s, formSource{"slice": {"wrong"}}, "form")
	require.Error(t, err)
}

func TestMappingArray(t *testing.T) {
	var s struct {
		Array [2]int `form:"array,default=9"`
	}

	// wrong default
	err := mappingByPtr(&s, formSource{}, "form")
	require.Error(t, err)

	// ok
	err = mappingByPtr(&s, formSource{"array": {"3", "4"}}, "form")
	require.NoError(t, err)
	assert.Equal(t, [2]int{3, 4}, s.Array)

	// error - not enough vals
	err = mappingByPtr(&s, formSource{"array": {"3"}}, "form")
	require.Error(t, err)

	// error - wrong value
	err = mappingByPtr(&s, formSource{"array": {"wrong"}}, "form")
	require.Error(t, err)
}

func TestMappingCollectionFormat(t *testing.T) {
	var s struct {
		SliceMulti []int  `form:"slice_multi" collection_format:"multi"`
		SliceCsv   []int  `form:"slice_csv" collection_format:"csv"`
		SliceSsv   []int  `form:"slice_ssv" collection_format:"ssv"`
		SliceTsv   []int  `form:"slice_tsv" collection_format:"tsv"`
		SlicePipes []int  `form:"slice_pipes" collection_format:"pipes"`
		ArrayMulti [2]int `form:"array_multi" collection_format:"multi"`
		ArrayCsv   [2]int `form:"array_csv" collection_format:"csv"`
		ArraySsv   [2]int `form:"array_ssv" collection_format:"ssv"`
		ArrayTsv   [2]int `form:"array_tsv" collection_format:"tsv"`
		ArrayPipes [2]int `form:"array_pipes" collection_format:"pipes"`
	}
	err := mappingByPtr(&s, formSource{
		"slice_multi": {"1", "2"},
		"slice_csv":   {"1,2"},
		"slice_ssv":   {"1 2"},
		"slice_tsv":   {"1	2"},
		"slice_pipes": {"1|2"},
		"array_multi": {"1", "2"},
		"array_csv":   {"1,2"},
		"array_ssv":   {"1 2"},
		"array_tsv":   {"1	2"},
		"array_pipes": {"1|2"},
	}, "form")
	require.NoError(t, err)

	assert.Equal(t, []int{1, 2}, s.SliceMulti)
	assert.Equal(t, []int{1, 2}, s.SliceCsv)
	assert.Equal(t, []int{1, 2}, s.SliceSsv)
	assert.Equal(t, []int{1, 2}, s.SliceTsv)
	assert.Equal(t, []int{1, 2}, s.SlicePipes)
	assert.Equal(t, [2]int{1, 2}, s.ArrayMulti)
	assert.Equal(t, [2]int{1, 2}, s.ArrayCsv)
	assert.Equal(t, [2]int{1, 2}, s.ArraySsv)
	assert.Equal(t, [2]int{1, 2}, s.ArrayTsv)
	assert.Equal(t, [2]int{1, 2}, s.ArrayPipes)
}

func TestMappingCollectionFormatInvalid(t *testing.T) {
	var s struct {
		SliceCsv []int `form:"slice_csv" collection_format:"xxx"`
	}
	err := mappingByPtr(&s, formSource{
		"slice_csv": {"1,2"},
	}, "form")
	require.Error(t, err)

	var s2 struct {
		ArrayCsv [2]int `form:"array_csv" collection_format:"xxx"`
	}
	err = mappingByPtr(&s2, formSource{
		"array_csv": {"1,2"},
	}, "form")
	require.Error(t, err)
}

func TestMappingMultipleDefaultWithCollectionFormat(t *testing.T) {
	var s struct {
		SliceMulti       []int     `form:",default=1;2;3" collection_format:"multi"`
		SliceCsv         []int     `form:",default=1;2;3" collection_format:"csv"`
		SliceSsv         []int     `form:",default=1 2 3" collection_format:"ssv"`
		SliceTsv         []int     `form:",default=1\t2\t3" collection_format:"tsv"`
		SlicePipes       []int     `form:",default=1|2|3" collection_format:"pipes"`
		ArrayMulti       [2]int    `form:",default=1;2" collection_format:"multi"`
		ArrayCsv         [2]int    `form:",default=1;2" collection_format:"csv"`
		ArraySsv         [2]int    `form:",default=1 2" collection_format:"ssv"`
		ArrayTsv         [2]int    `form:",default=1\t2" collection_format:"tsv"`
		ArrayPipes       [2]int    `form:",default=1|2" collection_format:"pipes"`
		SliceStringMulti []string  `form:",default=1;2;3" collection_format:"multi"`
		SliceStringCsv   []string  `form:",default=1;2;3" collection_format:"csv"`
		SliceStringSsv   []string  `form:",default=1 2 3" collection_format:"ssv"`
		SliceStringTsv   []string  `form:",default=1\t2\t3" collection_format:"tsv"`
		SliceStringPipes []string  `form:",default=1|2|3" collection_format:"pipes"`
		ArrayStringMulti [2]string `form:",default=1;2" collection_format:"multi"`
		ArrayStringCsv   [2]string `form:",default=1;2" collection_format:"csv"`
		ArrayStringSsv   [2]string `form:",default=1 2" collection_format:"ssv"`
		ArrayStringTsv   [2]string `form:",default=1\t2" collection_format:"tsv"`
		ArrayStringPipes [2]string `form:",default=1|2" collection_format:"pipes"`
	}
	err := mappingByPtr(&s, formSource{}, "form")
	require.NoError(t, err)

	assert.Equal(t, []int{1, 2, 3}, s.SliceMulti)
	assert.Equal(t, []int{1, 2, 3}, s.SliceCsv)
	assert.Equal(t, []int{1, 2, 3}, s.SliceSsv)
	assert.Equal(t, []int{1, 2, 3}, s.SliceTsv)
	assert.Equal(t, []int{1, 2, 3}, s.SlicePipes)
	assert.Equal(t, [2]int{1, 2}, s.ArrayMulti)
	assert.Equal(t, [2]int{1, 2}, s.ArrayCsv)
	assert.Equal(t, [2]int{1, 2}, s.ArraySsv)
	assert.Equal(t, [2]int{1, 2}, s.ArrayTsv)
	assert.Equal(t, [2]int{1, 2}, s.ArrayPipes)
	assert.Equal(t, []string{"1", "2", "3"}, s.SliceStringMulti)
	assert.Equal(t, []string{"1", "2", "3"}, s.SliceStringCsv)
	assert.Equal(t, []string{"1", "2", "3"}, s.SliceStringSsv)
	assert.Equal(t, []string{"1", "2", "3"}, s.SliceStringTsv)
	assert.Equal(t, []string{"1", "2", "3"}, s.SliceStringPipes)
	assert.Equal(t, [2]string{"1", "2"}, s.ArrayStringMulti)
	assert.Equal(t, [2]string{"1", "2"}, s.ArrayStringCsv)
	assert.Equal(t, [2]string{"1", "2"}, s.ArrayStringSsv)
	assert.Equal(t, [2]string{"1", "2"}, s.ArrayStringTsv)
	assert.Equal(t, [2]string{"1", "2"}, s.ArrayStringPipes)
}

func TestMappingStructField(t *testing.T) {
	var s struct {
		J struct {
			I int
		}
	}

	err := mappingByPtr(&s, formSource{"J": {`{"I": 9}`}}, "form")
	require.NoError(t, err)
	assert.Equal(t, 9, s.J.I)
}

func TestMappingPtrField(t *testing.T) {
	type ptrStruct struct {
		Key int64 `json:"key"`
	}

	type ptrRequest struct {
		Items []*ptrStruct `json:"items" form:"items"`
	}

	var err error

	// With 0 items.
	var req0 ptrRequest
	err = mappingByPtr(&req0, formSource{}, "form")
	require.NoError(t, err)
	assert.Empty(t, req0.Items)

	// With 1 item.
	var req1 ptrRequest
	err = mappingByPtr(&req1, formSource{"items": {`{"key": 1}`}}, "form")
	require.NoError(t, err)
	assert.Len(t, req1.Items, 1)
	assert.EqualValues(t, 1, req1.Items[0].Key)

	// With 2 items.
	var req2 ptrRequest
	err = mappingByPtr(&req2, formSource{"items": {`{"key": 1}`, `{"key": 2}`}}, "form")
	require.NoError(t, err)
	assert.Len(t, req2.Items, 2)
	assert.EqualValues(t, 1, req2.Items[0].Key)
	assert.EqualValues(t, 2, req2.Items[1].Key)
}

func TestMappingMapField(t *testing.T) {
	var s struct {
		M map[string]int
	}

	err := mappingByPtr(&s, formSource{"M": {`{"one": 1}`}}, "form")
	require.NoError(t, err)
	assert.Equal(t, map[string]int{"one": 1}, s.M)
}

func TestMappingIgnoredCircularRef(t *testing.T) {
	type S struct {
		S *S `form:"-"`
	}
	var s S

	err := mappingByPtr(&s, formSource{}, "form")
	require.NoError(t, err)
}

type customUnmarshalParamHex int

func (f *customUnmarshalParamHex) UnmarshalParam(param string) error {
	v, err := strconv.ParseInt(param, 16, 64)
	if err != nil {
		return err
	}
	*f = customUnmarshalParamHex(v)
	return nil
}

func TestMappingCustomUnmarshalParamHexWithFormTag(t *testing.T) {
	var s struct {
		Foo customUnmarshalParamHex `form:"foo"`
	}
	err := mappingByPtr(&s, formSource{"foo": {`f5`}}, "form")
	require.NoError(t, err)

	assert.EqualValues(t, 245, s.Foo)
}

func TestMappingCustomUnmarshalParamHexWithURITag(t *testing.T) {
	var s struct {
		Foo customUnmarshalParamHex `uri:"foo"`
	}
	err := mappingByPtr(&s, formSource{"foo": {`f5`}}, "uri")
	require.NoError(t, err)

	assert.EqualValues(t, 245, s.Foo)
}

func TestMappingCustomUnmarshalParamHexDefault(t *testing.T) {
	var s struct {
		Foo customUnmarshalParamHex `form:"foo,default=f5"`
	}
	err := mappingByPtr(&s, formSource{"foo": {}}, "form")
	require.NoError(t, err)

	assert.EqualValues(t, 0xf5, s.Foo)
}

type customUnmarshalParamType struct {
	Protocol string
	Path     string
	Name     string
}

func (f *customUnmarshalParamType) UnmarshalParam(param string) error {
	parts := strings.Split(param, ":")
	if len(parts) != 3 {
		return errors.New("invalid format")
	}
	f.Protocol = parts[0]
	f.Path = parts[1]
	f.Name = parts[2]
	return nil
}

func TestMappingCustomStructTypeWithFormTag(t *testing.T) {
	var s struct {
		FileData customUnmarshalParamType `form:"data"`
	}
	err := mappingByPtr(&s, formSource{"data": {`file:/foo:happiness`}}, "form")
	require.NoError(t, err)

	assert.Equal(t, "file", s.FileData.Protocol)
	assert.Equal(t, "/foo", s.FileData.Path)
	assert.Equal(t, "happiness", s.FileData.Name)
}

func TestMappingCustomStructTypeWithURITag(t *testing.T) {
	var s struct {
		FileData customUnmarshalParamType `uri:"data"`
	}
	err := mappingByPtr(&s, formSource{"data": {`file:/foo:happiness`}}, "uri")
	require.NoError(t, err)

	assert.Equal(t, "file", s.FileData.Protocol)
	assert.Equal(t, "/foo", s.FileData.Path)
	assert.Equal(t, "happiness", s.FileData.Name)
}

func TestMappingCustomPointerStructTypeWithFormTag(t *testing.T) {
	var s struct {
		FileData *customUnmarshalParamType `form:"data"`
	}
	err := mappingByPtr(&s, formSource{"data": {`file:/foo:happiness`}}, "form")
	require.NoError(t, err)

	assert.Equal(t, "file", s.FileData.Protocol)
	assert.Equal(t, "/foo", s.FileData.Path)
	assert.Equal(t, "happiness", s.FileData.Name)
}

func TestMappingCustomPointerStructTypeWithURITag(t *testing.T) {
	var s struct {
		FileData *customUnmarshalParamType `uri:"data"`
	}
	err := mappingByPtr(&s, formSource{"data": {`file:/foo:happiness`}}, "uri")
	require.NoError(t, err)

	assert.Equal(t, "file", s.FileData.Protocol)
	assert.Equal(t, "/foo", s.FileData.Path)
	assert.Equal(t, "happiness", s.FileData.Name)
}

type customPath []string

func (p *customPath) UnmarshalParam(param string) error {
	elems := strings.Split(param, "/")
	n := len(elems)
	if n < 2 {
		return errors.New("invalid format")
	}

	*p = elems
	return nil
}

func TestMappingCustomSliceUri(t *testing.T) {
	var s struct {
		FileData customPath `uri:"path"`
	}
	err := mappingByPtr(&s, formSource{"path": {`bar/foo`}}, "uri")
	require.NoError(t, err)

	assert.Equal(t, "bar", s.FileData[0])
	assert.Equal(t, "foo", s.FileData[1])
}

func TestMappingCustomSliceForm(t *testing.T) {
	var s struct {
		FileData customPath `form:"path"`
	}
	err := mappingByPtr(&s, formSource{"path": {`bar/foo`}}, "form")
	require.NoError(t, err)

	assert.Equal(t, "bar", s.FileData[0])
	assert.Equal(t, "foo", s.FileData[1])
}

func TestMappingCustomSliceStopsWhenError(t *testing.T) {
	var s struct {
		FileData customPath `form:"path"`
	}
	err := mappingByPtr(&s, formSource{"path": {"invalid"}}, "form")
	require.ErrorContains(t, err, "invalid format")
	require.Empty(t, s.FileData)
}

func TestMappingCustomSliceOfSliceUri(t *testing.T) {
	var s struct {
		FileData []customPath `uri:"path" collection_format:"csv"`
	}
	err := mappingByPtr(&s, formSource{"path": {"bar/foo,bar/foo/spam"}}, "uri")
	require.NoError(t, err)
	assert.Equal(t, []customPath{{"bar", "foo"}, {"bar", "foo", "spam"}}, s.FileData)
}

func TestMappingCustomSliceOfSliceForm(t *testing.T) {
	var s struct {
		FileData []customPath `form:"path" collection_format:"csv"`
	}
	err := mappingByPtr(&s, formSource{"path": {"bar/foo,bar/foo/spam"}}, "form")
	require.NoError(t, err)
	assert.Equal(t, []customPath{{"bar", "foo"}, {"bar", "foo", "spam"}}, s.FileData)
}

type objectID [12]byte

func (o *objectID) UnmarshalParam(param string) error {
	oid, err := convertTo(param)
	if err != nil {
		return err
	}

	*o = oid
	return nil
}

func convertTo(s string) (objectID, error) {
	var nilObjectID objectID
	if len(s) != 24 {
		return nilObjectID, errors.New("invalid format")
	}

	var oid [12]byte
	_, err := hex.Decode(oid[:], []byte(s))
	if err != nil {
		return nilObjectID, err
	}