package xtype

import "reflect"

func Is[T any](v any) bool {
    _, ok := v.(T)
    return ok
}

func Eq[A, B any]() bool {
    return reflect.TypeFor[A]() == reflect.TypeFor[B]()
}
