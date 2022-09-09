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

package bootstrapping

import (
	"bytes"
	"encoding/base64"
	"hash"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/pkg/errors"
)

// ChunkProcessor is called to process each chunk on ProcessFileWithChunks invocation.
// The chunk is base64 encoded while the hasNext argument is true if there are more file chunks.
type ChunkProcessor func(chunk string, hasNext bool) error

// ProcessFileWithChunks reads file in chunks and calls ChunkProcessor for each one.
// Breaks proccessing on chunk proccesssing error.
func ProcessFileWithChunks(filePath string, chunkSize int, processor ChunkProcessor, hash hash.Hash) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	buffer := make([]byte, chunkSize)

	fStat, sErr := os.Stat(filePath)
	if sErr != nil {
		return sErr
	}
	totalSize := fStat.Size()

	var readSize int64
	for {
		bytesread, err := file.Read(buffer)
		if err != nil {
			if err == io.EOF {
				break
			}
			return err

		}

		readSize = readSize + int64(bytesread)
		hash.Write(buffer[:bytesread])
		encoded := base64.StdEncoding.EncodeToString(buffer[:bytesread])

		err = processor(encoded, totalSize > readSize)
		if err != nil {
			return err
		}
	}
	return nil
}

// WriteFileChunk decodes and appends the chunk to the provided file.
// Updates the provided file hash.
func WriteFileChunk(file *os.File, fileHash hash.Hash, chunk string) error {
	decoded, err := base64.StdEncoding.DecodeString(chunk)
	if err != nil {
		return err
	}

	if _, err := file.Write(decoded); err != nil {
		return err
	}

	fileHash.Write([]byte(decoded))
	return file.Sync()
}

// CreateFileWithContent creates a file with the provided name and content.
func CreateFileWithContent(filePath string, content string) error {
	file, err := CreateFile(filePath)
	if err != nil {
		return err
	}

	defer file.Close()

	if len(content) > 0 {
		if _, err := file.WriteString(content); err != nil {
			return err
		}
	}

	return nil
}

// CreateFile creates a file with the provided name.
func CreateFile(filePath string) (*os.File, error) {
	dir := filepath.Dir(filePath)
	if _, statErr := os.Stat(dir); statErr != nil {
		if err := os.MkdirAll(dir, os.ModePerm); err != nil {
			return nil, err
		}
	}
	return os.Create(filePath)
}

// ExecScript executes a script file or command.
func ExecScript(script ...string) error {
	var cmdErr error
	var errBuffer bytes.Buffer

	cmd := exec.Command(script[0], script[1:]...)
	cmd.Stderr = &errBuffer

	err := cmd.Run()

	errLen := errBuffer.Len()
	if errLen > 0 {
		if errLen > 128 {
			errLen = 128
		}
		cmdErr = errors.Errorf("'%s' script error: %s:", script, errBuffer.Next(errLen))
	} else {
		if err != nil {
			cmdErr = errors.Errorf("'%s' script error: %s:", script, err)
		}
	}

	return cmdErr
}
