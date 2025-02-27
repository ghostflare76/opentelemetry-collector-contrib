// Copyright The OpenTelemetry Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//       http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package solacereceiver // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/solacereceiver"

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/config"
	"go.opentelemetry.io/collector/consumer/consumererror"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.uber.org/atomic"
)

// connectAndReceive with connect failure
// connectAndReceive with lifecycle validation
//   not started, connecting, connected, terminating, terminated, idle

func TestReceiveMessage(t *testing.T) {
	someError := errors.New("some error")

	validateMetrics := func(receivedMsgVal, droppedMsgVal, fatalUnmarshalling, reportedSpan interface{}) func(t *testing.T) {
		return func(t *testing.T) {
			validateReceiverMetrics(t, receivedMsgVal, droppedMsgVal, fatalUnmarshalling, reportedSpan)
		}
	}

	cases := []struct {
		name         string
		nextConsumer consumertest.Consumer
		// errors to return from messagingService.receive, unmarshaller.unmarshal, messagingService.ack and messagingService.nack
		receiveMessageErr, unmarshalErr, ackErr, nackErr error
		// whether or not to expect a nack call instead of an ack
		expectNack bool
		// expected error from receiveMessage
		expectedErr error
		// validate constraints after the fact
		validation func(t *testing.T)
	}{
		{ // no errors, expect no error, validate metrics
			name:       "Receive Message Success",
			validation: validateMetrics(1, nil, nil, 1),
		},
		{ // fail at receiveMessage and expect the error
			name:              "Receive Messages Error",
			receiveMessageErr: someError,
			expectedErr:       someError,
			validation:        validateMetrics(nil, nil, nil, nil),
		},
		{ // unmarshal error expecting the error to be swallowed, the message to be acknowledged, stats incremented
			name:         "Unmarshal Error",
			unmarshalErr: errUnknownTraceMessgeType,
			validation:   validateMetrics(1, 1, 1, nil),
		},
		{ // unmarshal error with wrong version expecting error to be propagated, message to be rejected
			name:         "Unmarshal Version Error",
			unmarshalErr: errUnknownTraceMessgeVersion,
			expectedErr:  errUnknownTraceMessgeVersion,
			expectNack:   true,
			validation:   validateMetrics(1, nil, 1, nil),
		},
		{ // expect forward to error and message to be swallowed with ack, no error returned
			name:         "Forward Permanent Error",
			nextConsumer: consumertest.NewErr(consumererror.NewPermanent(errors.New("a permanent error"))),
			validation:   validateMetrics(1, 1, nil, nil),
		},
		{ // expect forward to error and message to be rejected with nack, no error returned
			name:         "Forward Temporary Error",
			nextConsumer: consumertest.NewErr(errors.New("a temporary error")),
			expectNack:   true,
			validation:   validateMetrics(1, nil, nil, nil),
		},
		{ // expect forward to error and message to be swallowed with ack which fails returning an error
			name:         "Forward Permanent Error with Ack Error",
			nextConsumer: consumertest.NewErr(consumererror.NewPermanent(errors.New("a permanent error"))),
			ackErr:       someError,
			expectedErr:  someError,
			validation:   validateMetrics(1, 1, nil, nil),
		},
		{ // expect forward to error and message to be rejected with nack, no error returned
			name:         "Forward Temporary Error with Nack Error",
			nextConsumer: consumertest.NewErr(errors.New("a temporary error")),
			expectNack:   true,
			nackErr:      someError,
			expectedErr:  someError,
			validation:   validateMetrics(1, nil, nil, nil),
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			receiver, messagingService, unmarshaller := newReceiver()
			if testCase.nextConsumer != nil {
				receiver.nextConsumer = testCase.nextConsumer
			}

			msg := &inboundMessage{}
			trace := ptrace.NewTraces()

			// populate mock messagingService and unmarshaller functions, expecting them each to be called at most once
			var receiveMessagesCalled, ackCalled, nackCalled, unmarshalCalled bool
			messagingService.receiveMessageFunc = func(ctx context.Context) (*inboundMessage, error) {
				assert.False(t, receiveMessagesCalled)
				receiveMessagesCalled = true
				if testCase.receiveMessageErr != nil {
					return nil, testCase.receiveMessageErr
				}
				return msg, nil
			}
			messagingService.ackFunc = func(ctx context.Context, msg *inboundMessage) error {
				assert.False(t, ackCalled)
				ackCalled = true
				if testCase.ackErr != nil {
					return testCase.ackErr
				}
				return nil
			}
			messagingService.nackFunc = func(ctx context.Context, msg *inboundMessage) error {
				assert.False(t, nackCalled)
				nackCalled = true
				if testCase.nackErr != nil {
					return testCase.nackErr
				}
				return nil
			}
			unmarshaller.unmarshalFunc = func(msg *inboundMessage) (ptrace.Traces, error) {
				assert.False(t, unmarshalCalled)
				unmarshalCalled = true
				if testCase.unmarshalErr != nil {
					return ptrace.Traces{}, testCase.unmarshalErr
				}
				return trace, nil
			}

			err := receiver.receiveMessage(context.Background(), messagingService)
			if testCase.expectedErr != nil {
				assert.Equal(t, testCase.expectedErr, err)
			} else {
				assert.NoError(t, err)
			}
			assert.True(t, receiveMessagesCalled)
			if testCase.receiveMessageErr == nil {
				assert.True(t, unmarshalCalled)
				assert.Equal(t, testCase.expectNack, nackCalled)
				assert.Equal(t, !testCase.expectNack, ackCalled)
			}
			if testCase.validation != nil {
				testCase.validation(t)
			}
		})
	}
}

// receiveMessages ctx done return
func TestReceiveMessagesTerminateWithCtxDone(t *testing.T) {
	receiver, messagingService, unmarshaller := newReceiver()
	receiveMessagesCalled := false
	ctx, cancel := context.WithCancel(context.Background())
	msg := &inboundMessage{}
	trace := ptrace.NewTraces()
	messagingService.receiveMessageFunc = func(ctx context.Context) (*inboundMessage, error) {
		assert.False(t, receiveMessagesCalled)
		receiveMessagesCalled = true
		return msg, nil
	}
	ackCalled := false
	messagingService.ackFunc = func(ctx context.Context, msg *inboundMessage) error {
		assert.False(t, ackCalled)
		ackCalled = true
		cancel()
		return nil
	}
	unmarshalCalled := false
	unmarshaller.unmarshalFunc = func(msg *inboundMessage) (ptrace.Traces, error) {
		assert.False(t, unmarshalCalled)
		unmarshalCalled = true
		return trace, nil
	}
	err := receiver.receiveMessages(ctx, messagingService)
	assert.NoError(t, err)
	assert.True(t, receiveMessagesCalled)
	assert.True(t, unmarshalCalled)
	assert.True(t, ackCalled)
	validateReceiverMetrics(t, 1, nil, nil, 1)
}

func TestReceiverLifecycle(t *testing.T) {
	receiver, messagingService, _ := newReceiver()
	dialCalled := make(chan struct{})
	messagingService.dialFunc = func() error {
		validateMetric(t, viewReceiverStatus, receiverStateConnecting)
		close(dialCalled)
		return nil
	}
	closeCalled := make(chan struct{})
	messagingService.closeFunc = func(ctx context.Context) {
		validateMetric(t, viewReceiverStatus, receiverStateTerminating)
		close(closeCalled)
	}
	receiveMessagesCalled := make(chan struct{})
	messagingService.receiveMessageFunc = func(ctx context.Context) (*inboundMessage, error) {
		validateMetric(t, viewReceiverStatus, receiverStateConnected)
		close(receiveMessagesCalled)
		<-ctx.Done()
		return nil, errors.New("some error")
	}
	// start the receiver
	err := receiver.Start(context.Background(), nil)
	assert.NoError(t, err)
	assertChannelClosed(t, dialCalled)
	assertChannelClosed(t, receiveMessagesCalled)
	err = receiver.Shutdown(context.Background())
	assert.NoError(t, err)
	assertChannelClosed(t, closeCalled)
	validateMetric(t, viewReceiverStatus, receiverStateTerminated)
	// we error on receive message, so we should not report any metrics
	validateReceiverMetrics(t, nil, nil, nil, nil)
}

func TestReceiverDialFailureContinue(t *testing.T) {
	receiver, msgService, _ := newReceiver()
	dialErr := errors.New("Some dial error")
	const expectedAttempts = 3 // the number of attempts to perform prior to resolving
	dialCalled := 0
	factoryCalled := 0
	closeCalled := 0
	dialDone := make(chan struct{})
	factoryDone := make(chan struct{})
	closeDone := make(chan struct{})
	receiver.factory = func() messagingService {
		factoryCalled++
		if factoryCalled == expectedAttempts {
			close(factoryDone)
		}
		return msgService
	}
	msgService.dialFunc = func() error {
		dialCalled++
		if dialCalled == expectedAttempts {
			close(dialDone)
		}
		return dialErr
	}
	msgService.closeFunc = func(ctx context.Context) {
		closeCalled++
		// asset we never left connecting state prior to closing closeDone
		validateMetric(t, viewReceiverStatus, receiverStateConnecting)
		if closeCalled == expectedAttempts {
			close(closeDone)
			<-ctx.Done() // wait for ctx.Done
		}
	}
	// start the receiver
	err := receiver.Start(context.Background(), nil)
	assert.NoError(t, err)

	// expect factory to be called twice
	assertChannelClosed(t, factoryDone)
	// expect dial to be called twice
	assertChannelClosed(t, dialDone)
	// expect close to be called twice
	assertChannelClosed(t, closeDone)
	// assert failed reconnections
	validateMetric(t, viewFailedReconnections, expectedAttempts)

	err = receiver.Shutdown(context.Background())
	assert.NoError(t, err)
	validateMetric(t, viewReceiverStatus, receiverStateTerminated)
	// we error on dial, should never get to receive messages
	validateReceiverMetrics(t, nil, nil, nil, nil)
}

func TestReceiverUnmarshalVersionFailureExpectingDisable(t *testing.T) {
	receiver, msgService, unmarshaller := newReceiver()
	dialDone := make(chan struct{})
	nackCalled := make(chan struct{})
	closeDone := make(chan struct{})
	unmarshaller.unmarshalFunc = func(msg *inboundMessage) (ptrace.Traces, error) {
		return ptrace.Traces{}, errUnknownTraceMessgeVersion
	}
	msgService.dialFunc = func() error {
		// after we receive an unmarshalling version error, we should not call dial again
		msgService.dialFunc = func() error {
			t.Error("did not expect dial to be called again")
			return nil
		}
		close(dialDone)
		return nil
	}
	msgService.receiveMessageFunc = func(ctx context.Context) (*inboundMessage, error) {
		// we only expect a single receiveMessage call when unmarshal returns unknown version
		msgService.receiveMessageFunc = func(ctx context.Context) (*inboundMessage, error) {
			t.Error("did not expect receiveMessage to be called again")
			return nil, nil
		}
		return nil, nil
	}
	msgService.nackFunc = func(ctx context.Context, msg *inboundMessage) error {
		close(nackCalled)
		return nil
	}
	msgService.closeFunc = func(ctx context.Context) {
		close(closeDone)
	}
	// start the receiver
	err := receiver.Start(context.Background(), nil)
	assert.NoError(t, err)

	// expect dial to be called twice
	assertChannelClosed(t, dialDone)
	// expect nack to be called
	assertChannelClosed(t, nackCalled)
	// expect close to be called twice
	assertChannelClosed(t, closeDone)
	// we receive 1 message, encounter a fatal unmarshalling error and we nack the message so it is not actually dropped
	validateReceiverMetrics(t, 1, nil, 1, nil)
	// assert idle state
	validateMetric(t, viewReceiverStatus, receiverStateIdle)

	err = receiver.Shutdown(context.Background())
	assert.NoError(t, err)
	validateMetric(t, viewReceiverStatus, receiverStateTerminated)
}

func newReceiver() (*solaceTracesReceiver, *mockMessagingService, *mockUnmarshaller) {
	unmarshaller := &mockUnmarshaller{}
	service := &mockMessagingService{}
	messagingServiceFactory := func() messagingService {
		return service
	}
	receiver := &solaceTracesReceiver{
		settings:          componenttest.NewNopReceiverCreateSettings(),
		instanceID:        config.NewComponentID(componentType),
		config:            &Config{},
		nextConsumer:      consumertest.NewNop(),
		unmarshaller:      unmarshaller,
		factory:           messagingServiceFactory,
		shutdownWaitGroup: &sync.WaitGroup{},
		retryTimeout:      1 * time.Millisecond,
		terminating:       atomic.NewBool(false),
	}
	return receiver, service, unmarshaller
}

func validateReceiverMetrics(t *testing.T, receivedMsgVal, droppedMsgVal, fatalUnmarshalling, reportedSpan interface{}) {
	validateMetric(t, viewReceivedSpanMessages, receivedMsgVal)
	validateMetric(t, viewDroppedSpanMessages, droppedMsgVal)
	validateMetric(t, viewFatalUnmarshallingErrors, fatalUnmarshalling)
	validateMetric(t, viewReportedSpans, reportedSpan)
}

type mockMessagingService struct {
	dialFunc           func() error
	closeFunc          func(ctx context.Context)
	receiveMessageFunc func(ctx context.Context) (*inboundMessage, error)
	ackFunc            func(ctx context.Context, msg *inboundMessage) error
	nackFunc           func(ctx context.Context, msg *inboundMessage) error
}

func (m *mockMessagingService) dial() error {
	if m.dialFunc != nil {
		return m.dialFunc()
	}
	panic("did not expect dial to be called")
}

func (m *mockMessagingService) close(ctx context.Context) {
	if m.closeFunc != nil {
		m.closeFunc(ctx)
		return
	}
	panic("did not expect close to be called")
}

func (m *mockMessagingService) receiveMessage(ctx context.Context) (*inboundMessage, error) {
	if m.receiveMessageFunc != nil {
		return m.receiveMessageFunc(ctx)
	}
	panic("did not expect receiveMessage to be called")
}

func (m *mockMessagingService) ack(ctx context.Context, msg *inboundMessage) error {
	if m.ackFunc != nil {
		return m.ackFunc(ctx, msg)
	}
	panic("did not expect ack to be called")
}

func (m *mockMessagingService) nack(ctx context.Context, msg *inboundMessage) error {
	if m.nackFunc != nil {
		return m.nackFunc(ctx, msg)
	}
	panic("did not expect nack to be called")
}

type mockUnmarshaller struct {
	unmarshalFunc func(msg *inboundMessage) (ptrace.Traces, error)
}

func (m *mockUnmarshaller) unmarshal(message *inboundMessage) (ptrace.Traces, error) {
	if m.unmarshalFunc != nil {
		return m.unmarshalFunc(message)
	}
	panic("did not expect unmarshal to be called")
}
