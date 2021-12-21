// Code generated by mockery v0.0.0-dev. DO NOT EDIT.

package services

import mock "github.com/stretchr/testify/mock"

// MockPremiumDetection is an autogenerated mock type for the PremiumDetection type
type MockPremiumDetection struct {
	mock.Mock
}

// CanPublishTelemetry provides a mock function with given fields:
func (_m *MockPremiumDetection) CanPublishTelemetry() (bool, error) {
	ret := _m.Called()

	var r0 bool
	if rf, ok := ret.Get(0).(func() bool); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(bool)
	}

	var r1 error
	if rf, ok := ret.Get(1).(func() error); ok {
		r1 = rf()
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// IsPremiumActive provides a mock function with given fields:
func (_m *MockPremiumDetection) IsPremiumActive() (bool, error) {
	ret := _m.Called()

	var r0 bool
	if rf, ok := ret.Get(0).(func() bool); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(bool)
	}

	var r1 error
	if rf, ok := ret.Get(1).(func() error); ok {
		r1 = rf()
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}

// RequiresEulaAcceptance provides a mock function with given fields:
func (_m *MockPremiumDetection) RequiresEulaAcceptance() (bool, error) {
	ret := _m.Called()

	var r0 bool
	if rf, ok := ret.Get(0).(func() bool); ok {
		r0 = rf()
	} else {
		r0 = ret.Get(0).(bool)
	}

	var r1 error
	if rf, ok := ret.Get(1).(func() error); ok {
		r1 = rf()
	} else {
		r1 = ret.Error(1)
	}

	return r0, r1
}