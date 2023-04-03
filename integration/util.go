// Copyright (c) 2023 Contributors to the Eclipse Foundation
//
// See the NOTICE file(s) distributed with this work for additional
// information regarding copyright ownership.
//
// This program and the accompanying materials are made available under the
// terms of the Eclipse Public License 2.0 which is available at
// https://www.eclipse.org/legal/epl-2.0, or the Apache License, Version 2.0
// which is available at https://www.apache.org/licenses/LICENSE-2.0.
//
// SPDX-License-Identifier: EPL-2.0 OR Apache-2.0

package integration

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/eclipse-kanto/kanto/integration/util"
)

func GetBootstrapConfigStruct(path string) (util.BootstrapConfiguration, error) {
	cfg := util.BootstrapConfiguration{}
	content, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("unable to read file '%s': %v", path, err)
	}
	err = json.Unmarshal(content, &cfg)
	if err != nil {
		return cfg, fmt.Errorf("unable to unmarshal to json: %v", err)
	}
	return cfg, nil
}

func parseMap(value interface{}) (map[string]interface{}, error) {
	property, check := value.(map[string]interface{})
	if !check {
		return nil, fmt.Errorf("failed to parse the property to map")
	}
	return property, nil
}

func parseString(value interface{}) (string, error) {
	property, check := value.(string)
	if !check {
		return "", fmt.Errorf("failed to parse the property to string")
	}
	return property, nil
}
