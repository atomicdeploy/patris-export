package paradox

// Field represents a database field/column
type Field struct {
	Name string
	Type string
	Size int
}

// Record represents a database record/row
type Record map[string]interface{}
