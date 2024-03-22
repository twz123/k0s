/*
Copyright 2024 k0s authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1beta1_test

import (
	"encoding/json"
	"testing"
	"time"
)

func TestXxx(t *testing.T) {

	// Sample JSON data
	jsonData := []byte(`10000000`)

	var result time.Duration

	// Unmarshalling jsonData into result
	err := json.Unmarshal(jsonData, &result)
	if err != nil {
		t.Fatal("Error unmarshalling JSON:", err)
	}

	t.Errorf("%s", result)
}
