package model

// FieldAccess maps each field the caller may not change on a record to the
// reason the server would refuse it with (WL-REQ-1890). Only a detail route
// fills it.
type FieldAccess map[string]string
