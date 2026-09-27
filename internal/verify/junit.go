package verify

import (
	"encoding/xml"
	"fmt"
	"io"
)

// TestCase is one test result from a JUnit report.
type TestCase struct {
	ID      string
	Failed  bool
	Skipped bool
}

type junitSuite struct {
	Suites []junitSuite `xml:"testsuite"`
	Cases  []junitCase  `xml:"testcase"`
}

type junitCase struct {
	Classname string    `xml:"classname,attr"`
	Name      string    `xml:"name,attr"`
	Failure   *struct{} `xml:"failure"`
	Error     *struct{} `xml:"error"`
	Skipped   *struct{} `xml:"skipped"`
}

// ParseJUnit reads a JUnit XML report whose root is <testsuites> or <testsuite>.
func ParseJUnit(r io.Reader) ([]TestCase, error) {
	var root junitSuite
	if err := xml.NewDecoder(r).Decode(&root); err != nil {
		return nil, fmt.Errorf("parsing JUnit XML: %w", err)
	}
	var out []TestCase
	var walk func(junitSuite)
	walk = func(s junitSuite) {
		for _, c := range s.Cases {
			id := c.Name
			if c.Classname != "" {
				id = c.Classname + "." + c.Name
			}
			out = append(out, TestCase{ID: id, Failed: c.Failure != nil || c.Error != nil, Skipped: c.Skipped != nil})
		}
		for _, sub := range s.Suites {
			walk(sub)
		}
	}
	walk(root)
	return out, nil
}
