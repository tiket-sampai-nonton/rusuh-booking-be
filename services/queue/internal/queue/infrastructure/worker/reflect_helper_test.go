package worker_test

import "reflect"

func reflectTypeOfStub() reflect.Type { return reflect.TypeOf(&bookingStub{}) }
