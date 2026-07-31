// Package util provides small generic helpers shared across packages.
package util

// Ptr returns a pointer to the given value. Useful for OpenAPI-generated
// struct fields that are typed as pointers to distinguish "absent" from
// "zero value".
func Ptr[T any](v T) *T {
	return &v
}
