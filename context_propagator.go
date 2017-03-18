package main

import (
	"bufio"
	"bytes"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/printer"
	"go/token"
	"go/types"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Println("Usage: context_propagator.go <full package path>")
		os.Exit(1)
	}
	path := os.Args[1]

	fset := token.NewFileSet()

	pkgs, e := parser.ParseDir(fset, path, nil, parser.ParseComments)
	if e != nil {
		log.Fatal(e)
		return
	}

	astf := make([]*ast.File, 0)
	for _, pkg := range pkgs {
		fmt.Printf("Processing Package %v\n", pkg.Name)
		for _, f := range pkg.Files {
			//fmt.Printf("file %v\n", fp)
			astf = append(astf, f)
		}
	}

	config := &types.Config{
		Error:    func(err error) {},
		Importer: importer.Default(),
	}
	info := types.Info{
		Types: make(map[ast.Expr]types.TypeAndValue),
		Defs:  make(map[*ast.Ident]types.Object),
		Uses:  make(map[*ast.Ident]types.Object),
	}
	_, err := config.Check(path, fset, astf, &info)
	if err != nil {
		fmt.Printf("types.Config.Check error: %v\n", err)
	}

	for _, f := range astf {
		fileName := fset.Position(f.Pos()).Filename

		// Skip all tests files
		if strings.HasSuffix(fileName, "_test.go") {
			continue
		}

		filePath := strings.SplitAfter(fileName, "/")
		fileNameOnly := filePath[len(filePath)-1]

		// Skip all mock files
		if strings.HasPrefix(fileNameOnly, "mock_") || strings.HasSuffix(fileNameOnly, "mock.go") {
			continue
		}

		fmt.Printf("Processing File: %v\n\n", fileName)

		visitorNode := &PrintASTVisitor{tFSet: fset, astf: f, info: &info}
		ast.Walk(visitorNode, f)

		// Make sure to cleanup/reset the last function that's checked
		// TODO: This probably can be cleaner
		visitorNode.resetContexts()

		//ast.Print(fset, f)
		// If not modified, just skip the file writes
		if !visitorNode.modified {
			continue
		}

		var buf bytes.Buffer
		printer.Fprint(&buf, fset, f)

		tempFileName := fileName + ".tmp"
		f, err := os.Create(tempFileName)
		if err != nil {
			fmt.Println(err)
		}

		_, err = f.WriteString(buf.String())
		if err != nil {
			fmt.Println(err)
		}

		f.Sync()
		f.Close()

		tempFileName2 := fileName + ".tmp2"
		err = os.Rename(fileName, tempFileName2)
		if err != nil {
			fmt.Println(err)
		}

		err = os.Rename(tempFileName, fileName)
		if err != nil {
			fmt.Println(err)
		}

		err = os.Remove(tempFileName2)
		if err != nil {
			fmt.Println(err)
		}
	}

	// GoFmt Files
	_, err = exec.Command("sh", "-c", fmt.Sprintf("gofmt -s -w %v", path)).Output()
	if err != nil {
		fmt.Println(err)
	}
}

type PrintASTVisitor struct {
	tFSet    *token.FileSet
	astf     *ast.File
	info     *types.Info
	contexts []*contextInfo
	modified bool
}

type contextInfo struct {
	ident   *ast.Ident
	node    ast.Node
	foundAt string

	// This is added to allow reverting of unignores if it is not used
	// Probably can be done in a separate struct
	used         bool
	originalName string
}

func (v *PrintASTVisitor) PrintPossibleContext() {
	fmt.Println(" Earlier context values are:")
	for idx, contextInfo := range v.contexts {
		fmt.Printf("  %v: %v => %v\n", idx, contextInfo.ident, contextInfo.foundAt)
	}
	fmt.Println()
}

// Reset contexts and cleanup unused contexts
func (v *PrintASTVisitor) resetContexts() {
	for _, contextInfo := range v.contexts {
		if !contextInfo.used {
			contextInfo.ident.Name = contextInfo.originalName
		}
	}

	v.contexts = []*contextInfo{}
}

func (v *PrintASTVisitor) Visit(node ast.Node) ast.Visitor {
	if node != nil {
		switch node.(type) {
		case ast.Decl:
			declNode := node.(ast.Decl)
			if declNode != nil {
				switch declNode.(type) {
				case *ast.FuncDecl:
					v.resetContexts()

					funcDecl := declNode.(*ast.FuncDecl)

					contextAtParams := v.hasContextParam(funcDecl.Type.Params.List)

					// TODO: Extract these to a method and reuse it
					for _, contextAt := range contextAtParams {
						foundAt := fmt.Sprintf("Parameter for %v", funcDecl.Name.String())
						v.contexts = append(v.contexts, &contextInfo{
							ident:        contextAt,
							foundAt:      foundAt,
							node:         node,
							used:         true,
							originalName: contextAt.Name,
						})
					}

					funcDeclT := v.info.ObjectOf(funcDecl.Name)
					fmt.Printf("\nEntering: %v \n", funcDeclT)

					//printParamsAndBody(funcDecl.Type.Params.List, funcDecl.Body.List)

				case *ast.GenDecl:
					genDeclNode := declNode.(*ast.GenDecl)

					// We are only interested if this is a var declaration (var * = func....)
					if genDeclNode.Tok != token.VAR && len(genDeclNode.Specs) == 1 {
						break
					}

					// var decl uses ValueSpec
					genDeclValueSpec, ok := genDeclNode.Specs[0].(*ast.ValueSpec)
					if !ok || len(genDeclValueSpec.Values) != 1 {
						break
					}

					val := genDeclValueSpec.Values[0]
					funcLit, ok := val.(*ast.FuncLit)
					if !ok {
						break
					}

					v.resetContexts()

					// To print out the function signature
					genDeclT := v.info.TypeOf(val)
					fmt.Printf("\nEntering: %v %v\n", genDeclValueSpec.Names[0], genDeclT.Underlying())

					contextAtParams := v.hasContextParam(funcLit.Type.Params.List)

					for _, contextAt := range contextAtParams {
						foundAt := fmt.Sprintf("Parameter for %v", genDeclValueSpec.Names[0].String())

						v.contexts = append(v.contexts, &contextInfo{
							ident:        contextAt,
							node:         node,
							foundAt:      foundAt,
							used:         true,
							originalName: contextAt.Name,
						})
					}
					//printParamsAndBody(funcLit.Type.Params.List, funcLit.Body.List)
				}
			}

		case ast.Stmt:
			astNode := node.(ast.Stmt)

			switch astNode.(type) {
			case *ast.AssignStmt:
				assignStmt := astNode.(*ast.AssignStmt)

				//fmt.Printf(" Processing AssignStmt: %v\n", assignStmtStr)
				//fmt.Printf("  Processing RHS\n")
				contextResultsAtRhs := v.assignStmtRHS(node, assignStmt.Rhs)

				//fmt.Printf("  Processing LHS\n")
				contextAtLhs := v.assignStmtLHS(node, assignStmt.Lhs, contextResultsAtRhs)
				for contextIdx, contextName := range contextAtLhs {
					contextIdent := assignStmt.Lhs[contextIdx].(*ast.Ident)

					var buf bytes.Buffer
					printer.Fprint(&buf, v.tFSet, node)
					assignStmtStr := buf.String()

					v.contexts = append(v.contexts, &contextInfo{
						ident:        contextIdent,
						foundAt:      assignStmtStr,
						node:         node,
						used:         false,
						originalName: contextName,
					})
				}

			case *ast.ReturnStmt:
				returnStmt := astNode.(*ast.ReturnStmt)
				_ = v.assignStmtRHS(node, returnStmt.Results)
			}
		}
	}
	return v
}

// Process LHS to see if a new context is created
// returns the location of context in the expr list
func (v *PrintASTVisitor) assignStmtLHS(node ast.Node, assignStmtLHS []ast.Expr, contextResultsAtRhs []int) map[int]string {
	contextAt := map[int]string{}

	for idx, lhs := range assignStmtLHS {
		lhsIdent, ok := lhs.(*ast.Ident)
		// context should not be embedded
		// IMPROVE: Maybe allow it to be
		if !ok {
			continue
		}

		exitOuterLoop := false
		// If the context value is ignored, ask the users if
		// they want to un-ignore it
		if lhsIdent.Name == "_" {
			for _, resultIdx := range contextResultsAtRhs {
				if idx != resultIdx {
					continue
				}

				var buf bytes.Buffer
				printer.Fprint(&buf, v.tFSet, node)
				nodeStr := buf.String()

				// AUTOMATION: This is to attempt to automatically unignore tracer create child contexts
				tracerCall, _ := regexp.MatchString("tracer.CreateSpanFromContext", nodeStr)
				if tracerCall {
					contextAt[idx] = lhsIdent.Name
					lhsIdent.Name = "childCtx"
					exitOuterLoop = true
					continue
				}

				msg := fmt.Sprintf("  Unignore returned context value at position '%v' ? %v (y/n)\n  => ", idx, nodeStr)
				text := checkInput(msg, map[string]int{"y": 0, "n": 1})

				if text == "y" {
					msg := fmt.Sprint("  what do you want to name it?\n  => ")
					name := getInput(msg)

					name = strings.Replace(name, "\n", "", -1)

					contextAt[idx] = lhsIdent.Name

					lhsIdent.Name = name

					exitOuterLoop = true
					continue
				}
			}
		}

		if exitOuterLoop {
			continue
		}

		matched := v.isNetContextType(lhsIdent)
		if matched {
			contextAt[idx] = lhsIdent.Name
		}
	}

	return contextAt
}

// TODO: Rename this, it is used by assignStmt and returnStmt
// Process RHS to see if context is used
func (v *PrintASTVisitor) assignStmtRHS(node ast.Node, assignStmtRHS []ast.Expr) []int {
	// Only interested in Func calls
	assignCallExpr, ok := assignStmtRHS[0].(*ast.CallExpr)
	if !ok {
		return nil
	}

	v.hasContextArg(node, assignCallExpr.Args)

	contextAt := []int{}
	// TODO: Refine checks so that context.CancelFunc is not considered context
	// Check if the function call returns a context
	selectorExpr, ok := assignCallExpr.Fun.(*ast.SelectorExpr)
	if ok {

		selectorObj := v.info.ObjectOf(selectorExpr.Sel)

		//TODO: Investigate why this happens
		if selectorObj == nil {
			fmt.Printf("Skipped: %v\n", selectorExpr)
			return contextAt
		}

		selectorTypeObj := selectorObj.Type()
		selectorType := selectorTypeObj.(*types.Signature)
		results := selectorType.Results()

		for i := 0; i < results.Len(); i++ {
			named, ok := results.At(i).Type().(*types.Named)
			if ok {
				pkg := named.Obj().Pkg()

				// Investigate this. Happened during
				// Replace this context arg? phoneObj, err := pn.model.LoadPhoneNumber(context.Background(), phoneNumber) (y/n)
				// in grab-id/logic/login/phone_number.go
				if pkg == nil {
					// this is not from an imported package, can't be net/context
					continue
				}

				pkgStr := pkg.Path()
				if strings.HasSuffix(pkgStr, "golang.org/x/net/context") {
					contextAt = append(contextAt, i)

					// When it is a context.Context variable
				} else if strings.HasSuffix(pkgStr, "golang.org/x/net/context.Context") {
					contextAt = append(contextAt, i)
				}
			}
		}
	}
	identExpr, ok := assignCallExpr.Fun.(*ast.Ident)
	if ok {
		identObj := v.info.ObjectOf(identExpr)

		identType := identObj.Type().(*types.Signature)
		results := identType.Results()

		for i := 0; i < results.Len(); i++ {
			named, ok := results.At(i).Type().(*types.Named)
			if ok {
				pkg := named.Obj().Pkg()
				if pkg == nil {
					// this is not from an imported package, can't be net/context
					continue
				}

				pkgStr := pkg.Path()
				if strings.HasSuffix(pkgStr, "golang.org/x/net/context") {
					contextAt = append(contextAt, i)

					// When it is a context.Context variable
				} else if strings.HasSuffix(pkgStr, "golang.org/x/net/context.Context") {
					contextAt = append(contextAt, i)
				}
			}
		}
	}

	return contextAt
}

// Check if a context.Context is passed in as a param
// This only works properly (has been tested) when there's only one single variable defined
//   Works: ctx context.Context
//   Does Not Work: ctx1, ctx2 context.Context
// returns the location of context in the field list
func (v *PrintASTVisitor) hasContextParam(params []*ast.Field) []*ast.Ident {
	contexts := []*ast.Ident{}

	for _, p := range params {
		selectorExpr, ok := p.Type.(*ast.SelectorExpr)
		// All context params are selectorExpr
		if !ok {
			continue
		}

		xIdent := selectorExpr.X.(*ast.Ident)
		matched := v.isNetContextType(xIdent)
		if matched {
			contexts = append(contexts, p.Names[0])
			// selIdent := selectorExpr.Sel
			// fmt.Printf(" Context Param Type: %v %v\n", p.Names[0], selIdent)
		}
	}

	return contexts
}

// Check if a context type is part of the argument to the function call
// TODO: Handle AssignStmt can be nested inside another AssignStmt
func (v *PrintASTVisitor) hasContextArg(node ast.Node, args []ast.Expr) {
	// No replacement possible, don't bother scanning
	if len(v.contexts) == 0 {
		return
	}

	for idx, a := range args {
		var ident *ast.Ident
		switch a.(type) {
		case *ast.CallExpr:
			aCallExpr := a.(*ast.CallExpr)

			selectorExpr, ok := aCallExpr.Fun.(*ast.SelectorExpr)
			// Ignore if this is not a SelectorExpr as context values are either Idents or call
			// to funcs in the context package
			if !ok {
				continue
			}

			ident = selectorExpr.X.(*ast.Ident)
		case *ast.Ident:
			ident = a.(*ast.Ident)
		default:
			continue
		}

		matched := v.isNetContextType(ident)
		if matched {

			// If there is only one replacement option and it is equal to
			// the current value we're checking, skip.
			if len(v.contexts) == 1 && v.contexts[0].ident.Name == ident.Name {
				continue
			}

			var buf bytes.Buffer
			printer.Fprint(&buf, v.tFSet, node)
			nodeStr := buf.String()

			fmt.Printf("At: %#v.\n", nodeStr)
			v.PrintPossibleContext()

			// AUTOMATE TEST: Already replace context with the last seen context
			if true {
				lastContextIdx := len(v.contexts) - 1
				args[idx] = ast.NewIdent(v.contexts[lastContextIdx].ident.Name)
				v.contexts[lastContextIdx].used = true

				v.modified = true

				continue
			}

			msg := fmt.Sprintf(" Replace this context arg? %v (y/n)\n  => ", nodeStr)
			text := checkInput(msg, map[string]int{"y": 0, "n": 1})

			if text == "y" {
				msg = fmt.Sprint(" Which context to replace with?\n  => ")

				validInputs := make(map[string]int)
				for idx, _ := range v.contexts {
					validInputs[strconv.Itoa(idx)] = idx
				}
				text := checkInput(msg, validInputs)

				repIdx, err := strconv.ParseInt(text, 10, 0)
				if err != nil {
					fmt.Printf("  error when converting %v to number. err: %v", text, err)
					os.Exit(1)
				}

				args[idx] = ast.NewIdent(v.contexts[repIdx].ident.Name)
				v.contexts[repIdx].used = true

				v.modified = true
			}

			fmt.Println()
		}
	}
}

func (v *PrintASTVisitor) isNetContextType(ident *ast.Ident) bool {
	identInfo := v.info.ObjectOf(ident)

	var pkgStr string
	switch identInfo.(type) {
	case *types.PkgName:
		contextPkg := identInfo.Parent().Lookup(ident.Name)
		contextPkgName := contextPkg.(*types.PkgName)

		pkgStr = contextPkgName.Imported().Path()
	case *types.Var:
		// This can be either a definition or use of an existing definition
		def := v.info.Defs[ident]
		if def != nil {
			// A plain string check is not ideal but it is the easiest
			pkgStr = def.Type().String()
			break
		}

		defType := v.info.Types[ident]
		if defType.Type != nil {
			// A plain string check is not ideal but it is the easiest
			pkgStr = defType.Type.String()
		}
	}

	if strings.HasSuffix(pkgStr, "golang.org/x/net/context") {
		return true

		// When it is a context.Context variable
	} else if strings.HasSuffix(pkgStr, "golang.org/x/net/context.Context") {
		return true
	}

	return false
}

func printParamsAndBody(params []*ast.Field, body []ast.Stmt) {
	// Outputs: params: ctx &{context Context}
	for _, p := range params {
		fmt.Printf("  params: %v %v\n", p.Names[0], p.Type)
	}

	// Outputs: FuncBody: &{[err] 339 := [0xc42009a6c0]}
	for _, bodyStmt := range body {
		fmt.Printf("    FuncBody: %v\n", bodyStmt)
	}
}

// Outputs: paramsDetailed: ctx github.com/myteksi/go/vendor/golang.org/x/net/context.Context
func printDetailedParams(genType types.Type) {
	signatureType, ok := genType.Underlying().(*types.Signature)
	if ok {
		tuples := signatureType.Params()
		tuplesLen := tuples.Len()
		for i := 0; i < tuplesLen; i++ {
			p := tuples.At(i)
			fmt.Printf("  paramsDetailed: %v %v \n", p.Name(), p.Type())
		}
	}
}

func getInput(msg string) string {
	reader := bufio.NewReader(os.Stdin)
	fmt.Print(msg)

	text, _ := reader.ReadString('\n')
	text = strings.Replace(text, "\n", "", -1)

	return text
}

func checkInput(msg string, validInputs map[string]int) string {
	for {
		input := getInput(msg)

		_, exists := validInputs[input]
		if exists {
			return input
		} else {
			fmt.Printf("Invalid input %v, try again\n", input)
		}
	}
}
