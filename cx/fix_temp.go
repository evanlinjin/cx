package cxcore

import (
	"github.com/skycoin/cx/cx/ast"
)

// WriteToSlice is used to create slices in the backend, i.e. not by calling `append`
// in a CX program, but rather by the CX code itself. This function is used by
// affordances, serialization and to store OS input arguments.
func WriteToSlice(off int, inp []byte) int {
	// TODO: Check all these parses from/to int32/int.
	var inputSliceLen int32
	if off != 0 {
		inputSliceLen = ast.GetSliceLen(int32(off))
	}

	inpLen := len(inp)
	// We first check if a resize is needed. If a resize occurred
	// the address of the new slice will be stored in `newOff` and will
	// be different to `off`.
	newOff := ast.SliceResizeEx(int32(off), inputSliceLen+1, inpLen)

	// Copy the data from the old slice at `off` to `newOff`.
	ast.SliceCopyEx(int32(newOff), int32(off), inputSliceLen+1, inpLen)

	// Write the new slice element `inp` to the slice located at `newOff`.
	ast.SliceAppendWrite(int32(newOff), inp, inputSliceLen)
	return newOff

}

