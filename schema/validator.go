/*
 *     Copyright 2025 The CNCF ModelPack Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *      http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package schema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sync"

	v1 "github.com/modelpack/model-spec/specs-go/v1"
	"github.com/santhosh-tekuri/jsonschema/v5"
)

// Validator wraps a media type string identifier and implements validation against a JSON schema.
type Validator string

// Validate validates the given reader against the schema of the wrapped media type.
func (v Validator) Validate(src io.Reader) error {
	// run the media type specific validation
	if fn, ok := validateByMediaType[v]; ok {
		if fn == nil {
			return fmt.Errorf("internal error: mapValidate is nil for %s", string(v))
		}
		// buffer the src so the media type validation and the schema validation can both read it
		buf, err := io.ReadAll(src)
		if err != nil {
			return fmt.Errorf("failed to read input: %w", err)
		}
		src = bytes.NewReader(buf)
		err = fn(buf)
		if err != nil {
			return err
		}
	}

	// json schema validation
	return v.validateSchema(src)
}

// compiledSchemas caches compiled *jsonschema.Schema values by Validator.
// Compilation is expensive; caching avoids repeating it on every Validate call.
var compiledSchemas sync.Map

func (v Validator) validateSchema(src io.Reader) error {
	if _, ok := specs[v]; !ok {
		return fmt.Errorf("no validator available for %s", string(v))
	}

	schema, err := loadCompiledSchema(v)
	if err != nil {
		return err
	}

	var input interface{}
	if err := json.NewDecoder(src).Decode(&input); err != nil {
		return fmt.Errorf("unable to parse json to validate: %w", err)
	}
	if err := schema.Validate(input); err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}
	return nil
}

// loadCompiledSchema returns a compiled schema for the given validator,
// using a sync.Map cache so compilation happens only once per validator.
func loadCompiledSchema(v Validator) (*jsonschema.Schema, error) {
	if cached, ok := compiledSchemas.Load(v); ok {
		return cached.(*jsonschema.Schema), nil
	}

	schema, err := compileSchema(v)
	if err != nil {
		return nil, err
	}

	actual, loaded := compiledSchemas.LoadOrStore(v, schema)
	if loaded {
		return actual.(*jsonschema.Schema), nil
	}
	return schema, nil
}

func compileSchema(v Validator) (*jsonschema.Schema, error) {
	c := jsonschema.NewCompiler()
	c.AssertFormat = true

	dir, err := specFS.ReadDir(".")
	if err != nil {
		return nil, fmt.Errorf("spec embedded directory could not be loaded: %w", err)
	}
	for _, file := range dir {
		if file.IsDir() {
			continue
		}
		specBuf, err := specFS.ReadFile(file.Name())
		if err != nil {
			return nil, fmt.Errorf("could not read spec file %s: %w", file.Name(), err)
		}
		if err := c.AddResource(file.Name(), bytes.NewReader(specBuf)); err != nil {
			return nil, fmt.Errorf("failed to add spec file %s: %w", file.Name(), err)
		}
		if len(specURLs[file.Name()]) == 0 {
			return nil, fmt.Errorf("spec file has no aliases: %s", file.Name())
		}
		for _, specURL := range specURLs[file.Name()] {
			if err := c.AddResource(specURL, bytes.NewReader(specBuf)); err != nil {
				return nil, fmt.Errorf("failed to add spec file %s as url %s: %w", file.Name(), specURL, err)
			}
		}
	}

	schema, err := c.Compile(specs[v])
	if err != nil {
		return nil, fmt.Errorf("failed to compile schema %s: %w", string(v), err)
	}
	return schema, nil
}

type validateFunc func([]byte) error

var validateByMediaType = map[Validator]validateFunc{
	ValidatorMediaTypeModelConfig: validateConfig,
}

func validateConfig(buf []byte) error {
	model := v1.Model{}

	err := json.Unmarshal(buf, &model)
	if err != nil {
		return fmt.Errorf("config format mismatch: %w", err)
	}

	return nil
}
