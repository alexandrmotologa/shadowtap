package storage

import (
	"encoding/xml"
	"fmt"
	"os"

	"github.com/alexandrmotologa/shadowtap/pkg/diff"
)

// JUnitTestSuites root model for JUnit XML.
type JUnitTestSuites struct {
	XMLName  xml.Name         `xml:"testsuites"`
	Name     string           `xml:"name,attr"`
	Tests    int              `xml:"tests,attr"`
	Failures int              `xml:"failures,attr"`
	Time     string           `xml:"time,attr"`
	Suites   []JUnitTestSuite `xml:"testsuite"`
}

// JUnitTestSuite models a suite per route or overall run.
type JUnitTestSuite struct {
	Name      string          `xml:"name,attr"`
	Tests     int             `xml:"tests,attr"`
	Failures  int             `xml:"failures,attr"`
	Cases     []JUnitTestCase `xml:"testcase"`
}

// JUnitTestCase models an individual shadow diff verification.
type JUnitTestCase struct {
	Name      string        `xml:"name,attr"`
	ClassName string        `xml:"classname,attr"`
	Time      string        `xml:"time,attr"`
	Failure   *JUnitFailure `xml:"failure,omitempty"`
}

// JUnitFailure describes the cause of failure.
type JUnitFailure struct {
	Message string `xml:"message,attr"`
	Type    string `xml:"type,attr"`
	Content string `xml:",chardata"`
}

// WriteJUnitReport serializes test results into a standard JUnit XML file.
func WriteJUnitReport(filePath string, pairs []diff.ShadowPair, srr float64, maxSRR float64) error {
	total := len(pairs)
	failures := 0

	testcases := make([]JUnitTestCase, 0, total)
	for _, p := range pairs {
		tc := JUnitTestCase{
			Name:      fmt.Sprintf("%s %s [%s]", p.Method, p.Path, p.ID),
			ClassName: "shadowtap.regression",
			Time:      fmt.Sprintf("%.3f", p.Primary.LatencyMs/1000.0),
		}

		if !p.Diff.IsMatch {
			failures++
			msg := fmt.Sprintf("Semantic drift detected (distance: %.3f)", p.Diff.SemanticDistance)
			details := ""
			for _, m := range p.Diff.Mismatches {
				details += fmt.Sprintf("[%s] %s: %s\n", m.Kind, m.Path, m.Message)
			}
			tc.Failure = &JUnitFailure{
				Message: msg,
				Type:    "SemanticRegression",
				Content: details,
			}
		}
		testcases = append(testcases, tc)
	}

	suite := JUnitTestSuite{
		Name:     "shadowtap-verification",
		Tests:    total,
		Failures: failures,
		Cases:    testcases,
	}

	root := JUnitTestSuites{
		Name:     "shadowtap-audit",
		Tests:    total,
		Failures: failures,
		Time:     "0.0",
		Suites:   []JUnitTestSuite{suite},
	}

	data, err := xml.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}

	xmlHeader := []byte("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	return os.WriteFile(filePath, append(xmlHeader, data...), 0644)
}
