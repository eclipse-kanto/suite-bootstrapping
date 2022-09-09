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

import "net/http"

// MessageErrorCode defines the error code.
type MessageErrorCode string

const (
	// MessagesParameterInvalid defines the invalid parameter code.
	MessagesParameterInvalid MessageErrorCode = "messages:parameter.invalid"
	// MessagesExecutionFailed defines the execution failed code.
	MessagesExecutionFailed MessageErrorCode = "messages:execution.failed"

	// StatusBadRequest defines the bad request status code.
	StatusBadRequest = http.StatusBadRequest
	// StatusInternalError defines the internal server error status code.
	StatusInternalError = http.StatusInternalServerError
)

// MessageError contains a message error data.
type MessageError struct {
	ErrorCode MessageErrorCode `json:"error"`
	Status    int              `json:"status"`
}

// NewMessageParameterInvalidError creates invalid parameter message error.
func NewMessageParameterInvalidError() *MessageError {
	return &MessageError{
		ErrorCode: MessagesParameterInvalid,
		Status:    StatusBadRequest,
	}
}

// NewMessageInternalError creates internal server message error.
func NewMessageInternalError() *MessageError {
	return &MessageError{
		ErrorCode: MessagesExecutionFailed,
		Status:    StatusInternalError,
	}
}
