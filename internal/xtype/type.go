package xtype

import "reflect"

func Is[A, B any]() bool {
	return reflect.TypeFor[A]() == reflect.TypeFor[B]()
}
