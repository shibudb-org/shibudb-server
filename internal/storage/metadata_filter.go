package storage

import (
	"fmt"
	"strings"
)

// Metadata field types declared at space creation for a filterable Flat vector
// space. Numeric values (int and float) are indexed and compared as float64,
// which is exact for integers up to 2^53.
const (
	MetadataTypeString = "string"
	MetadataTypeInt    = "int"
	MetadataTypeFloat  = "float"
)

// Filter operators supported by the filterable Flat vector engine.
const (
	FilterOpEq      = "eq"
	FilterOpIn      = "in"
	FilterOpAnd     = "and"
	FilterOpOr      = "or"
	FilterOpNot     = "not"
	FilterOpGt      = "gt"
	FilterOpGte     = "gte"
	FilterOpLt      = "lt"
	FilterOpLte     = "lte"
	FilterOpBetween = "between"
)

// MetadataFieldSpec declares one indexable metadata field for a vector space.
type MetadataFieldSpec struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// MetadataFilter is a recursive predicate over indexed metadata fields. Leaf
// nodes (eq/in/range) carry a Field and value(s); boolean nodes (and/or/not)
// carry child Filters.
type MetadataFilter struct {
	Op      string            `json:"op"`
	Field   string            `json:"field,omitempty"`
	Value   any               `json:"value,omitempty"`
	Values  []any             `json:"values,omitempty"`
	Filters []*MetadataFilter `json:"filters,omitempty"`
}

// ValidateFieldSpecs ensures field names are unique/non-empty and types are allowed.
func ValidateFieldSpecs(specs []MetadataFieldSpec) error {
	seen := make(map[string]struct{}, len(specs))
	for _, spec := range specs {
		name := strings.TrimSpace(spec.Name)
		if name == "" {
			return fmt.Errorf("metadata field name must not be empty")
		}
		if _, dup := seen[name]; dup {
			return fmt.Errorf("duplicate metadata field %q", name)
		}
		seen[name] = struct{}{}
		switch spec.Type {
		case MetadataTypeString, MetadataTypeInt, MetadataTypeFloat:
		default:
			return fmt.Errorf("metadata field %q has invalid type %q (allowed: string, int, float)", name, spec.Type)
		}
	}
	return nil
}

// ValidateFilter checks that a filter references only declared fields and that
// each operator is allowed for the referenced field's type.
func ValidateFilter(filter *MetadataFilter, specs []MetadataFieldSpec) error {
	return validateFilter(filter, fieldSpecTypes(specs))
}

func fieldSpecTypes(specs []MetadataFieldSpec) map[string]string {
	types := make(map[string]string, len(specs))
	for _, spec := range specs {
		types[spec.Name] = spec.Type
	}
	return types
}

func isNumericMetadataType(t string) bool {
	return t == MetadataTypeInt || t == MetadataTypeFloat
}

func validateFilter(filter *MetadataFilter, types map[string]string) error {
	if filter == nil {
		return fmt.Errorf("filter must not be nil")
	}
	switch filter.Op {
	case FilterOpAnd, FilterOpOr:
		if len(filter.Filters) == 0 {
			return fmt.Errorf("%q filter requires at least one sub-filter", filter.Op)
		}
		for _, sub := range filter.Filters {
			if err := validateFilter(sub, types); err != nil {
				return err
			}
		}
		return nil
	case FilterOpNot:
		if len(filter.Filters) != 1 {
			return fmt.Errorf("%q filter requires exactly one sub-filter", filter.Op)
		}
		return validateFilter(filter.Filters[0], types)
	case FilterOpEq, FilterOpIn, FilterOpGt, FilterOpGte, FilterOpLt, FilterOpLte, FilterOpBetween:
		fieldType, ok := types[filter.Field]
		if !ok {
			return fmt.Errorf("filter field %q is not an indexed metadata field", filter.Field)
		}
		if filter.Op != FilterOpEq && filter.Op != FilterOpIn && !isNumericMetadataType(fieldType) {
			return fmt.Errorf("range operator %q is not allowed on non-numeric field %q", filter.Op, filter.Field)
		}
		switch filter.Op {
		case FilterOpIn:
			if len(filter.Values) == 0 {
				return fmt.Errorf("%q filter on %q requires a non-empty values list", filter.Op, filter.Field)
			}
		case FilterOpBetween:
			if len(filter.Values) != 2 {
				return fmt.Errorf("%q filter on %q requires exactly two values [low, high]", filter.Op, filter.Field)
			}
		default:
			if filter.Value == nil {
				return fmt.Errorf("%q filter on %q requires a value", filter.Op, filter.Field)
			}
		}
		return nil
	default:
		return fmt.Errorf("unknown filter operator %q", filter.Op)
	}
}
