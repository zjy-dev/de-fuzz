package coverage

import (
	"fmt"
	"os"
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_cpp "github.com/tree-sitter/tree-sitter-cpp/bindings/go"
)

// CppAbstractor implements CodeAbstractor for C/C++ source files.
type CppAbstractor struct {
	parser *tree_sitter.Parser
	lang   *tree_sitter.Language
}

// NewCppAbstractor creates a new C/C++ code abstractor.
func NewCppAbstractor() *CppAbstractor {
	parser := tree_sitter.NewParser()
	lang := tree_sitter.NewLanguage(tree_sitter_cpp.Language())
	parser.SetLanguage(lang)

	return &CppAbstractor{
		parser: parser,
		lang:   lang,
	}
}

// SupportedLanguages returns the file extensions this abstractor supports.
func (ca *CppAbstractor) SupportedLanguages() []string {
	return []string{".c", ".cpp", ".cc", ".cxx", ".h", ".hpp", ".hxx"}
}

// Abstract processes the uncovered input and generates abstracted code.
func (ca *CppAbstractor) Abstract(input *UncoveredInput) (*AbstractedOutput, error) {
	output := &AbstractedOutput{
		Functions: make([]AbstractedFunction, 0),
	}

	for _, file := range input.Files {
		// Read source file
		sourceCode, err := os.ReadFile(file.FilePath)
		if err != nil {
			// Add error for all functions in this file
			for _, fn := range file.Functions {
				output.Functions = append(output.Functions, AbstractedFunction{
					FilePath:       file.FilePath,
					FunctionName:   fn.FunctionName,
					DemangledName:  fn.DemangledName,
					UncoveredLines: fn.UncoveredLines,
					Error:          fmt.Errorf("failed to read file: %w", err),
				})
			}
			continue
		}

		// Parse source code
		tree := ca.parser.Parse(sourceCode, nil)
		if tree == nil {
			// Add error for all functions in this file
			for _, fn := range file.Functions {
				output.Functions = append(output.Functions, AbstractedFunction{
					FilePath:       file.FilePath,
					FunctionName:   fn.FunctionName,
					DemangledName:  fn.DemangledName,
					UncoveredLines: fn.UncoveredLines,
					Error:          fmt.Errorf("failed to parse source code"),
				})
			}
			continue
		}

		// Process each function
		for _, fn := range file.Functions {
			abstracted := ca.abstractFunction(tree, sourceCode, fn)
			abstracted.FilePath = file.FilePath
			output.Functions = append(output.Functions, abstracted)
		}

		tree.Close()
	}

	return output, nil
}

// abstractFunction generates abstracted code for a single function.
func (ca *CppAbstractor) abstractFunction(tree *tree_sitter.Tree, sourceCode []byte, fn UncoveredFunction) AbstractedFunction {
	result := AbstractedFunction{
		FunctionName:   fn.FunctionName,
		DemangledName:  fn.DemangledName,
		UncoveredLines: fn.UncoveredLines,
	}

	// Find the function in the AST
	root := tree.RootNode()

	// Try to find by demangled name first, then by function name
	searchName := fn.DemangledName
	if searchName == "" {
		searchName = fn.FunctionName
	}

	funcNode := ca.findFunction(root, sourceCode, searchName)
	if funcNode == nil {
		result.Error = fmt.Errorf("function '%s' not found", searchName)
		return result
	}

	// Build uncovered lines map
	uncoveredLines := make(map[int]bool)
	for _, line := range fn.UncoveredLines {
		uncoveredLines[line] = true
	}

	// Identify uncovered nodes
	uncoveredNodes := make(map[uintptr]bool)
	ca.identifyUncoveredNodes(funcNode, sourceCode, uncoveredLines, uncoveredNodes)

	// Identify critical path (control flow nodes on path to uncovered code)
	criticalNodes := make(map[uintptr]bool)
	ca.identifyCriticalPath(funcNode, uncoveredNodes, criticalNodes)

	// Generate abstracted code
	abstractedCode := ca.generateAbstractedCode(funcNode, sourceCode, uncoveredNodes, criticalNodes)
	result.AbstractedCode = abstractedCode

	return result
}

// findFunction searches for a function definition by name in the AST.
func (ca *CppAbstractor) findFunction(node *tree_sitter.Node, sourceCode []byte, functionName string) *tree_sitter.Node {
	if node == nil {
		return nil
	}

	if node.Kind() == "function_definition" {
		declarator := node.ChildByFieldName("declarator")
		if declarator != nil {
			// Navigate through pointer/reference declarators
			funcDeclarator := declarator
			for funcDeclarator != nil && (funcDeclarator.Kind() == "pointer_declarator" || funcDeclarator.Kind() == "reference_declarator") {
				funcDeclarator = funcDeclarator.ChildByFieldName("declarator")
			}

			if funcDeclarator != nil && funcDeclarator.Kind() == "function_declarator" {
				nameNode := funcDeclarator.ChildByFieldName("declarator")
				if nameNode != nil {
					nodeName := ca.getNodeText(nameNode, sourceCode)
					if nodeName == functionName {
						return node
					}
				}
			}
		}
	}

	// Recursively search children
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if result := ca.findFunction(child, sourceCode, functionName); result != nil {
			return result
		}
	}

	return nil
}

// identifyUncoveredNodes identifies statement-level nodes containing uncovered lines.
func (ca *CppAbstractor) identifyUncoveredNodes(funcNode *tree_sitter.Node, sourceCode []byte, uncoveredLines map[int]bool, uncoveredNodes map[uintptr]bool) {
	statementTypes := map[string]bool{
		"expression_statement": true,
		"return_statement":     true,
		"declaration":          true,
		"break_statement":      true,
		"continue_statement":   true,
		"goto_statement":       true,
	}

	var traverse func(*tree_sitter.Node)
	traverse = func(node *tree_sitter.Node) {
		if node == nil {
			return
		}

		startLine := int(node.StartPosition().Row) + 1
		endLine := int(node.EndPosition().Row) + 1

		// Check if this node contains uncovered lines
		hasUncoveredLine := false
		for line := startLine; line <= endLine; line++ {
			if uncoveredLines[line] {
				hasUncoveredLine = true
				break
			}
		}

		if hasUncoveredLine {
			// If this is a statement node, mark it as uncovered
			if statementTypes[node.Kind()] {
				uncoveredNodes[node.Id()] = true
			} else {
				// Otherwise, recurse into children
				for i := uint(0); i < node.ChildCount(); i++ {
					traverse(node.Child(i))
				}
			}
		}
	}

	traverse(funcNode)
}

// identifyCriticalPath identifies control flow nodes on the path to uncovered nodes.
func (ca *CppAbstractor) identifyCriticalPath(funcNode *tree_sitter.Node, uncoveredNodes map[uintptr]bool, criticalNodes map[uintptr]bool) {
	controlFlowTypes := map[string]bool{
		"if_statement":     true,
		"else_clause":      true,
		"switch_statement": true,
		"case_statement":   true,
		"for_statement":    true,
		"while_statement":  true,
		"do_statement":     true,
	}

	// For each uncovered node, trace back to function root and mark control flow nodes
	for nodeId := range uncoveredNodes {
		// Find the node by ID
		var findNode func(*tree_sitter.Node) *tree_sitter.Node
		findNode = func(n *tree_sitter.Node) *tree_sitter.Node {
			if n == nil {
				return nil
			}
			if n.Id() == nodeId {
				return n
			}
			for i := uint(0); i < n.ChildCount(); i++ {
				if result := findNode(n.Child(i)); result != nil {
					return result
				}
			}
			return nil
		}

		uncoveredNode := findNode(funcNode)
		if uncoveredNode == nil {
			continue
		}

		// Trace back to function root
		current := uncoveredNode
		for current != nil && current.Id() != funcNode.Id() {
			if controlFlowTypes[current.Kind()] {
				criticalNodes[current.Id()] = true
			}
			current = current.Parent()
		}
	}
}

// generateAbstractedCode generates the abstracted source code.
func (ca *CppAbstractor) generateAbstractedCode(funcNode *tree_sitter.Node, sourceCode []byte, uncoveredNodes map[uintptr]bool, criticalNodes map[uintptr]bool) string {
	var result strings.Builder

	// Get function signature
	funcType := funcNode.ChildByFieldName("type")
	funcDeclarator := funcNode.ChildByFieldName("declarator")
	funcBody := funcNode.ChildByFieldName("body")

	if funcType != nil {
		result.WriteString(ca.getNodeText(funcType, sourceCode))
		result.WriteString(" ")
	}

	if funcDeclarator != nil {
		result.WriteString(ca.getNodeText(funcDeclarator, sourceCode))
		result.WriteString(" ")
	}

	if funcBody != nil {
		bodyCode := ca.traverseNode(funcBody, sourceCode, uncoveredNodes, criticalNodes, 0)
		result.WriteString(bodyCode)
	}

	return result.String()
}

// traverseNode recursively traverses the AST and generates abstracted code.
func (ca *CppAbstractor) traverseNode(node *tree_sitter.Node, sourceCode []byte, uncoveredNodes map[uintptr]bool, criticalNodes map[uintptr]bool, indent int) string {
	if node == nil {
		return ""
	}

	nodeType := node.Kind()

	// If this is an uncovered node (leaf), keep it fully
	if uncoveredNodes[node.Id()] {
		return ca.getNodeText(node, sourceCode)
	}

	// Handle if statements
	if nodeType == "if_statement" {
		return ca.handleIfStatement(node, sourceCode, uncoveredNodes, criticalNodes, indent)
	}

	// Handle switch statements
	if nodeType == "switch_statement" {
		return ca.handleSwitchStatement(node, sourceCode, uncoveredNodes, criticalNodes, indent)
	}

	// Handle case statements
	if nodeType == "case_statement" {
		return ca.handleCaseStatement(node, sourceCode, uncoveredNodes, criticalNodes, indent)
	}

	// Handle break statements
	if nodeType == "break_statement" {
		return ca.getNodeText(node, sourceCode)
	}

	// Handle compound statements
	if nodeType == "compound_statement" {
		return ca.handleCompoundStatement(node, sourceCode, uncoveredNodes, criticalNodes, indent)
	}

	// For other nodes, recurse into children
	if node.ChildCount() > 0 && nodeType != "translation_unit" {
		var parts strings.Builder
		for i := uint(0); i < node.ChildCount(); i++ {
			child := node.Child(i)
			childText := ca.traverseNode(child, sourceCode, uncoveredNodes, criticalNodes, indent)
			if childText != "" {
				parts.WriteString(childText)
			}
		}
		resultText := parts.String()
		if strings.TrimSpace(resultText) != "" {
			return resultText
		}
	}

	return ""
}

// handleIfStatement handles if statement abstraction.
func (ca *CppAbstractor) handleIfStatement(node *tree_sitter.Node, sourceCode []byte, uncoveredNodes map[uintptr]bool, criticalNodes map[uintptr]bool, indent int) string {
	var parts strings.Builder

	conditionNode := node.ChildByFieldName("condition")
	consequenceNode := node.ChildByFieldName("consequence")
	alternativeNode := node.ChildByFieldName("alternative")

	if conditionNode != nil {
		parts.WriteString("if ")
		parts.WriteString(ca.getNodeText(conditionNode, sourceCode))
		parts.WriteString(" ")
	}

	if consequenceNode != nil {
		if ca.containsUncovered(consequenceNode, uncoveredNodes) {
			parts.WriteString(ca.traverseNode(consequenceNode, sourceCode, uncoveredNodes, criticalNodes, indent))
		} else {
			parts.WriteString("// ...")
		}
	}

	if alternativeNode != nil {
		if alternativeNode.Kind() == "else_clause" {
			parts.WriteString("\n")
			parts.WriteString(strings.Repeat(" ", indent))
			parts.WriteString("else ")

			for i := uint(0); i < alternativeNode.ChildCount(); i++ {
				child := alternativeNode.Child(i)
				if child.Kind() == "if_statement" {
					parts.WriteString(ca.traverseNode(child, sourceCode, uncoveredNodes, criticalNodes, indent))
					break
				} else if child.Kind() != "else" {
					if ca.containsUncovered(child, uncoveredNodes) {
						parts.WriteString(ca.traverseNode(child, sourceCode, uncoveredNodes, criticalNodes, indent))
					} else {
						parts.WriteString("// ...")
					}
					break
				}
			}
		}
	}

	return parts.String()
}

// handleSwitchStatement handles switch statement abstraction.
func (ca *CppAbstractor) handleSwitchStatement(node *tree_sitter.Node, sourceCode []byte, uncoveredNodes map[uintptr]bool, criticalNodes map[uintptr]bool, indent int) string {
	var parts strings.Builder

	condition := node.ChildByFieldName("condition")
	body := node.ChildByFieldName("body")

	if condition != nil {
		parts.WriteString("switch ")
		parts.WriteString(ca.getNodeText(condition, sourceCode))
		parts.WriteString(" ")
	}

	if body != nil {
		parts.WriteString("{\n")
		for i := uint(0); i < body.ChildCount(); i++ {
			child := body.Child(i)
			if child.Kind() == "case_statement" || child.Kind() == "break_statement" {
				childStr := ca.traverseNode(child, sourceCode, uncoveredNodes, criticalNodes, indent)
				if childStr != "" {
					parts.WriteString(strings.Repeat(" ", indent))
					parts.WriteString(childStr)
				}
			}
		}
		parts.WriteString(strings.Repeat(" ", indent))
		parts.WriteString("}")
	}

	return parts.String()
}

// handleCaseStatement handles case statement abstraction.
func (ca *CppAbstractor) handleCaseStatement(node *tree_sitter.Node, sourceCode []byte, uncoveredNodes map[uintptr]bool, criticalNodes map[uintptr]bool, indent int) string {
	var parts strings.Builder

	value := node.ChildByFieldName("value")

	if value != nil {
		parts.WriteString("case ")
		parts.WriteString(ca.getNodeText(value, sourceCode))
		parts.WriteString(":")
	} else {
		parts.WriteString("default:")
	}

	if ca.containsUncovered(node, uncoveredNodes) {
		parts.WriteString("\n")
		for i := uint(0); i < node.ChildCount(); i++ {
			child := node.Child(i)
			childType := child.Kind()
			if childType != "case" && childType != "default" && childType != ":" && childType != "value" {
				childStr := ca.traverseNode(child, sourceCode, uncoveredNodes, criticalNodes, indent+4)
				if childStr != "" {
					parts.WriteString(strings.Repeat(" ", indent+4))
					parts.WriteString(childStr)
					if childType == "expression_statement" {
						parts.WriteString("\n")
					}
				}
			}
		}
	} else {
		// Check if this is a fallthrough case
		caseLabelTypes := map[string]bool{
			"case": true, "default": true, ":": true, "value": true,
			"number_literal": true, "string_literal": true,
			"identifier": true, "char_literal": true,
		}

		hasStatements := false
		for i := uint(0); i < node.ChildCount(); i++ {
			child := node.Child(i)
			if !caseLabelTypes[child.Kind()] {
				hasStatements = true
				break
			}
		}

		if hasStatements {
			parts.WriteString(" // ...\n")
		} else {
			parts.WriteString("\n")
		}
	}

	return parts.String()
}

// handleCompoundStatement handles compound statement (block) abstraction.
func (ca *CppAbstractor) handleCompoundStatement(node *tree_sitter.Node, sourceCode []byte, uncoveredNodes map[uintptr]bool, criticalNodes map[uintptr]bool, indent int) string {
	var parts strings.Builder
	parts.WriteString("{")

	prevWasControl := false
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child.Kind() != "{" && child.Kind() != "}" {
			if prevWasControl && child.Kind() == "if_statement" {
				parts.WriteString("\n")
			}

			childStr := ca.traverseNode(child, sourceCode, uncoveredNodes, criticalNodes, indent+4)
			if childStr != "" && strings.TrimSpace(childStr) != "" {
				parts.WriteString("\n")
				parts.WriteString(strings.Repeat(" ", indent+4))
				parts.WriteString(childStr)
			}

			controlFlowTypes := map[string]bool{
				"if_statement": true, "switch_statement": true,
				"for_statement": true, "while_statement": true,
			}
			prevWasControl = controlFlowTypes[child.Kind()]
		}
	}

	parts.WriteString("\n")
	parts.WriteString(strings.Repeat(" ", indent))
	parts.WriteString("}")

	return parts.String()
}

// containsUncovered checks if a node contains uncovered code.
func (ca *CppAbstractor) containsUncovered(node *tree_sitter.Node, uncoveredNodes map[uintptr]bool) bool {
	if node == nil {
		return false
	}

	if uncoveredNodes[node.Id()] {
		return true
	}

	for i := uint(0); i < node.ChildCount(); i++ {
		if ca.containsUncovered(node.Child(i), uncoveredNodes) {
			return true
		}
	}

	return false
}

// getNodeText extracts text for a node.
func (ca *CppAbstractor) getNodeText(node *tree_sitter.Node, sourceCode []byte) string {
	if node == nil {
		return ""
	}
	start := node.StartByte()
	end := node.EndByte()
	if int(end) > len(sourceCode) {
		end = uint(len(sourceCode))
	}
	return string(sourceCode[start:end])
}
