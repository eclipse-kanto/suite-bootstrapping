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
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/eclipse-kanto/kanto/integration/util"
)

func getBootstrapConfigStruct(path string) (util.BootstrapConfiguration, error) {
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

func createBootstrappingResponsePayload(
	cfg *util.ConnectorConfiguration, requestID string) (map[string]interface{}, error) {

	jsonContents, err := json.MarshalIndent(cfg, "", "\t")
	if err != nil {
		return nil, err
	}

	jsonDataHasher := sha256.New()
	rawEncoded := base64.StdEncoding.EncodeToString(jsonContents)
	_, err = jsonDataHasher.Write(jsonContents)
	if err != nil {
		return nil, err
	}

	return map[string]interface{}{
		"requestId": requestID,
		"chunk":     rawEncoded,
		"hash":      hex.EncodeToString(jsonDataHasher.Sum(nil)),
	}, nil
}

func getTenantURL(address, tenantId string) string {
	return fmt.Sprintf("%s/v1/devices/%s/",
		strings.TrimSuffix(address, "/"), tenantId)
}

func backupRestoreFile(src, dst string, restore bool) error {
	if src != "" && dst != "" {
		if err := util.CopyFile(src, dst); err != nil {
			return err
		}

		if restore {
			if err := os.Remove(src); err != nil {
				return err
			}
		}
	}
	return nil
}
