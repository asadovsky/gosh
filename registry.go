package gosh

// Inspired by https://github.com/golang/appengine/blob/master/delay/delay.go.

import (
	"encoding/gob"
	"fmt"
	"reflect"
)

type Fn struct {
	name  string
	value reflect.Value
}

var (
	fns       = map[string]*Fn{}
	errorType = reflect.TypeOf((*error)(nil)).Elem()
)

// TODO: Switch to using len(fns) as name, and maybe drop the name argument, if
// it turns out that initialization order is deterministic.
func Register(name string, i interface{}) *Fn {
	if _, ok := fns[name]; ok {
		panic(fmt.Errorf("already registered: %s", name))
	}
	v := reflect.ValueOf(i)
	t := v.Type()
	if t.Kind() != reflect.Func {
		panic(fmt.Errorf("not a function: %v", t.Kind()))
	}
	if t.NumOut() > 1 || t.NumOut() == 1 && t.Out(0) != errorType {
		panic(fmt.Errorf("function must return an error or nothing"))
	}
	// Register the function's args with gob. Needed because Shell.Fn() takes
	// interface{} arguments.
	for i := 0; i < t.NumIn(); i++ {
		// Note: Clients are responsible for registering any concrete types stored
		// inside interface{} arguments.
		if t.In(i).Kind() == reflect.Interface {
			continue
		}
		gob.Register(reflect.Zero(t.In(i)).Interface())
	}
	fn := &Fn{name: name, value: v}
	fns[name] = fn
	return fn
}

func Call(name string, args ...interface{}) error {
	if fn, ok := fns[name]; !ok {
		return fmt.Errorf("unknown function: %s", name)
	} else {
		return fn.Call(args...)
	}
}

func (fn *Fn) Call(args ...interface{}) error {
	t := fn.value.Type()
	in := []reflect.Value{}
	for i, arg := range args {
		var av reflect.Value
		if arg != nil {
			av = reflect.ValueOf(arg)
		} else {
			// Client passed nil; construct the zero value for this argument based on
			// the function signature.
			at := t.In(i)
			if t.IsVariadic() && i == t.NumIn()-1 {
				at = at.Elem()
			}
			av = reflect.Zero(at)
		}
		in = append(in, av)
	}
	out := fn.value.Call(in)
	if t.NumOut() == 1 && !out[0].IsNil() {
		return out[0].Interface().(error)
	}
	return nil
}
