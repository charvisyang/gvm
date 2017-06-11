package gvm

import (
	"strings"
	"reflect"
)

const DEFAULT_VM_STACK_SIZE  = 512

type Thread struct {
	id          uint
	name        string
	vmStack     []*StackFrame
}

func newThread(name string) *Thread  {
	return &Thread{
		name: name,
		vmStack: make([]*StackFrame, 0, DEFAULT_VM_STACK_SIZE)}
}


type StackFrame struct {
	method *Method
	// if this frame is current frame, the pc is for the pc of this thread;
	// otherwise, it is a snapshot one since the last time
	pc uint32
	localVariables      []j_any
	// operand stack
	operandStack        []j_any
}

func NewStackFrame(method *Method) *StackFrame {
	stackFrame := &StackFrame{
		method: method,
		pc: 0,
		localVariables: make([]j_any, method.maxLocals), // local variables have no initial values
		operandStack: make([]j_any, 0, method.maxStack)}
	return stackFrame
}

func (this *StackFrame) loadVar(index uint) j_any {
	value := this.localVariables[index]
	return value
}

func (this *StackFrame) storeVar(index uint, value j_any)  {
	this.localVariables[index] = value
}

func (this *Thread) invokeStaticMethod(index uint16) {
	f := this.peekFrame()
	m := f.method
	c := m.class

	method := c.constantPool[index].resolve().(*RuntimeConstantMethodrefInfo).method.resolve()
	parameterCount := len(method.Type.parameterTypes)
	params := make([]j_any, parameterCount)
	// read parameters
	for i := parameterCount-1; i >= 0; i-- {
		params[i] = f.pop()
	}

	if method.isNative() {
		result := this.invokeNativeMethod(method, params...)
		if method.Type.returnType != VOID_TYPE {
			f.push(result)
		}
	} else {
		frame := NewStackFrame(method)
		// pass parameters
		for j := parameterCount-1; j >= 0; j--  {
			frame.storeVar(uint(j), params[j])
		}

		this.pushFrame(frame)
	}

}

func (this *Thread) invokeVitrualMethod(index uint16) {
	f := this.peekFrame()
	m := f.method
	c := m.class
	method := c.constantPool[index].resolve().(*RuntimeConstantMethodrefInfo).method.resolve()
	if method.isStatic() {
		fatal("Not an instance method")
	}
	parameterCount := len(method.Type.parameterTypes) + 1 // with an extra objectref: this
	params := make([]j_any, parameterCount)
	for i := parameterCount-1; i >= 0; i-- {
		params[i] = f.pop()
	}
	// get objectref and target method
	objectref := params[0].(*j_object)
	overridenMethod := objectref.class.findMethod(method.name + method.descriptor).resolve()

	if method.isNative() {
		result := this.invokeNativeMethod(overridenMethod, params...)
		if overridenMethod.Type.returnType != VOID_TYPE {
			f.push(result)
		}
	} else {
		frame := NewStackFrame(overridenMethod)
		// pass parameters
		for j := 0; j < parameterCount; j++ {
			frame.storeVar(uint(j), params[j])
		}

		this.pushFrame(frame)
	}
}

// like invokevirtual with objectref, but don't find along the inheritance
func (this *Thread) invokeSpecialMethod(index uint16)  {
	f := this.peekFrame()
	m := f.method
	c := m.class
	method := c.constantPool[index].resolve().(*RuntimeConstantMethodrefInfo).method.resolve()
	parameterCount := len(method.Type.parameterTypes) + 1 // with an extra objectref: this
	params := make([]j_any, parameterCount)
	for i := parameterCount-1; i >= 0; i-- {
		params[i] = f.pop()
	}

	if method.isNative() {
		result := this.invokeNativeMethod(method, params...)
		if method.Type.returnType != VOID_TYPE {
			f.push(result)
		}
	} else {
		frame := NewStackFrame(method)
		// pass parameters
		for j := 0; j < parameterCount; j++ {
			frame.storeVar(uint(j), params[j])
		}

		this.pushFrame(frame)
	}
}

func (this *Thread) invokeNativeMethod(method *Method, params ... j_any) j_any {
	if !method.isNative() {
		fatal("Not a native method")
	}
	debug("🍺invoke native method %s#%s%s 😎\n", method.class.thisClassName, method.name, method.descriptor)
	name := "Java_" + strings.Replace(method.class.thisClassName + "_" + method.name, "/", "_", -1)
	funcs := NativeFunctions
	if _, ok := funcs[name]; !ok {
		fatal( "%s does not exist.", name)
	}
	if len(params) != funcs[name].Type().NumIn() {
		fatal( "The number of params is not adapted.")
	}
	in := make([]reflect.Value, len(params))
	for k, param := range params {
		in[k] = reflect.ValueOf(param)
	}
	result := funcs[name].Call(in)
	if len(result) == 0 {
		return j_any(nil)
	}
	return result[0].Interface().(j_any)
}

/*
Parameters are passed in a reversed order from operand stack in JVM
 */
func (this *StackFrame) passParameters(callee *StackFrame)  {
	method := callee.method.resolve()
	start := len(method.Type.parameterTypes) - 1
	end := 0
	if !method.isStatic() {
		start += 1
		end += 1
	}
	for i := start ;i >= end; i-- {
		callee.storeVar(uint(i), this.pop())
	}
	if !method.isStatic() {
		callee.storeVar(0, this.pop()) // this reference for instance method
	}
}

func (this *StackFrame) passReturn(caller *StackFrame)  {
	caller.push(this.pop())
}

func (this *StackFrame) getField(objectref *j_object, index uint16) j_any {
	i := this.method.class.constantPool[index].resolve().(*RuntimeConstantFieldrefInfo).field.index
	return objectref.fields[i]
}

func (this *StackFrame) putField(objectref *j_object, index uint16, value j_any) {
	i := this.method.class.constantPool[index].resolve().(*RuntimeConstantFieldrefInfo).field.index
	objectref.fields[i] = value
}

func (this *StackFrame) push(jany j_any)  {
	operandStackSize := len(this.operandStack)
	this.operandStack = this.operandStack[:operandStackSize+1]
	this.operandStack[operandStackSize] = jany
}

func (this *StackFrame) pop() j_any {
	operandStackSize := len(this.operandStack)
	jany := this.operandStack[operandStackSize-1]
	this.operandStack[operandStackSize-1] = nil
	this.operandStack = this.operandStack[:operandStackSize-1]
	return jany
}

func (this *StackFrame) peek() j_any {
	operandStackSize := len(this.operandStack)
	jany := this.operandStack[operandStackSize-1]
	return jany
}

func (this *Thread) pushFrame(stackFrame *StackFrame)  {
	size := len(this.vmStack)
	if size == DEFAULT_VM_STACK_SIZE {
		fatal("Stack Overflow")
	}
	this.vmStack = this.vmStack[:size+1]
	this.vmStack[size] = stackFrame
}

func (this *Thread) popFrame()  {
	size := len(this.vmStack)
	if size == 0 {
		return
	}
	this.vmStack = this.vmStack[:size-1]
}

func (this *Thread) peekFrame() *StackFrame  {
	size := len(this.vmStack)
	return this.vmStack[size-1]
}