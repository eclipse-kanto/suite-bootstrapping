// Copyright (c) 2022 Contributors to the Eclipse Foundation
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

package bootstrapping_test

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	bs "github.com/eclipse-kanto/suite-bootstrapping/internal/bootstrapping"
	"github.com/eclipse-kanto/suite-connector/util"
)

const testTempDir = "test"

func TestCreateFile(t *testing.T) {
	filePath := testTempDir + "/file_create.json"
	defer func() {
		os.Remove(filePath)
		os.Remove(testTempDir)
	}()

	content := `{"key1":true, "key2":5}`
	err := bs.CreateFileWithContent(filePath, content)
	require.NoError(t, err)
	assert.True(t, util.FileExists(filePath))
}

func TestProcessFileWithChunks(t *testing.T) {
	inputFileName := "file_utils_test.go"
	outputFileName := "test_file_process_with_chunks.out"
	file, err := os.Create(outputFileName)
	defer os.Remove(outputFileName)
	require.NoError(t, err)

	var chunksCount, nonLastChunks int
	chunkSize := 512
	outputHash := sha256.New()
	processedHash := sha256.New()
	err = bs.ProcessFileWithChunks(inputFileName, chunkSize, func(chunk string, hasNext bool) error {
		chunksCount = chunksCount + 1
		if hasNext {
			nonLastChunks = nonLastChunks + 1
		}
		return bs.WriteFileChunk(file, outputHash, chunk)
	}, processedHash)
	file.Close()
	require.NoError(t, err)

	// assert sizes
	inputSize := fileSize(t, inputFileName)
	assert.Equal(t, inputSize, fileSize(t, outputFileName))

	// assert chunks checksum
	inputFileHash := fileHash(t, inputFileName)
	assert.Equal(t, inputFileHash, hex.EncodeToString(processedHash.Sum(nil)))
	assert.Equal(t, inputFileHash, hex.EncodeToString(outputHash.Sum(nil)))

	// assert chunks count
	assert.LessOrEqual(t, inputSize, int64(chunksCount*chunkSize))
	assert.GreaterOrEqual(t, inputSize, int64((chunksCount-1)*chunkSize))

	assert.Equal(t, chunksCount-1, nonLastChunks)
}

func fileSize(t *testing.T, fileName string) int64 {
	inputFile, err := os.Stat(fileName)
	require.NoError(t, err, fileName)
	return inputFile.Size()
}

func fileHash(t *testing.T, fileName string) string {
	hasher := sha256.New()

	file, err := os.Open(fileName)
	require.NoError(t, err, fileName)
	defer file.Close()

	_, err = io.Copy(hasher, file)
	require.NoError(t, err, fileName)

	return hex.EncodeToString(hasher.Sum(nil))
}

func TestExecScriptNonExisting(t *testing.T) {
	script := "nonexisting.bat"
	require.False(t, util.FileExists(script))

	err := bs.ExecScript(script)
	require.Error(t, err)
}

func TestExecScript(t *testing.T) {
	output := "empty.txt"

	var script []string
	if runtime.GOOS == "windows" {
		script = []string{"cmd", "/k", "echo test > " + output}
	} else if runtime.GOOS == "linux" {
		script = []string{"touch", output}
	} else {
		return
	}

	require.False(t, util.FileExists(output))
	defer os.Remove(output)

	err := bs.ExecScript(script...)
	require.NoError(t, err)
	assert.True(t, util.FileExists(output), output)

	output = "invalid.txt"
	err = bs.ExecScript(script...)
	require.NoError(t, err)
	assert.False(t, util.FileExists(output), output)
}

func TestExecScriptCmd(t *testing.T) {
	script := []string{}
	if runtime.GOOS == "windows" {
		script = append(script, "cmd")
	}

	script = append(script, "ls")
	err := bs.ExecScript(script...)
	require.NoError(t, err)

	script = append(script, "-lah")
	err = bs.ExecScript(script...)
	require.NoError(t, err)
}
