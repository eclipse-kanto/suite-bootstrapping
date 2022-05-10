// Copyright (c) 2022 Contributors to the Eclipse Foundation
//
// See the NOTICE file(s) distributed with this work for additional
// information regarding copyright ownership.
//
// This program and the accompanying materials are made available under the
// terms of the Eclipse Public License 2.0 which is available at
// http://www.eclipse.org/legal/epl-2.0
//
// SPDX-License-Identifier: EPL-2.0

package bootstrapping_test

import (
	"testing"

	bs "github.com/eclipse-kanto/suite-bootstrapping/internal/bootstrapping"
	"github.com/stretchr/testify/assert"
)

func TestMessageParameterInvalidError(t *testing.T) {
	msgErr := bs.NewMessageParameterInvalidError()
	assert.Equal(t, bs.MessagesParameterInvalid, msgErr.ErrorCode)
	assert.Equal(t, 400, msgErr.Status)
}

func TestMessageInternalError(t *testing.T) {
	msgErr := bs.NewMessageInternalError()
	assert.Equal(t, bs.MessagesExecutionFailed, msgErr.ErrorCode)
	assert.Equal(t, 500, msgErr.Status)
}
