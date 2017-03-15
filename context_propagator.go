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

	pkgs, e := parser.ParseDir(fset, path, nil, 0)
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

		// Skip all mock files
		if strings.HasPrefix(fileName, "mock") && strings.HasSuffix(fileName, "mock.go") {
			continue
		}

		fmt.Printf("Processing File: %v\n", fileName)

		ast.Walk(&PrintASTVisitor{tFSet: fset, astf: f, info: &info}, f)

		// TODO: Output to file and replace original file
		var buf bytes.Buffer
		printer.Fprint(&buf, fset, f)
		fmt.Println(buf.String())
		//ast.Print(fset, f)
	}
}

type PrintASTVisitor struct {
	tFSet    *token.FileSet
	astf     *ast.File
	info     *types.Info
	contexts []*contextInfo
}

type contextInfo struct {
	ident   *ast.Ident
	foundAt string
}

func (v *PrintASTVisitor) PrintPossibleContext() {
	fmt.Println(" Earlier context values are:")
	for idx, contextInfo := range v.contexts {
		fmt.Printf("  %v:\n   %v =>\n    %v\n", idx, contextInfo.ident, contextInfo.foundAt)
	}
}

func (v *PrintASTVisitor) Visit(node ast.Node) ast.Visitor {
	if node != nil {
		switch node.(type) {
		case ast.Decl:
			declNode := node.(ast.Decl)
			if declNode != nil {
				switch declNode.(type) {
				case *ast.FuncDecl:
					// Entering new scope, reset context records
					v.contexts = []*contextInfo{}

					funcDecl := declNode.(*ast.FuncDecl)

					contextAtParams := v.hasContextParam(funcDecl.Type.Params.List)

					// TODO: Extract these to a method and reuse it
					for _, contextAt := range contextAtParams {
						foundAt := fmt.Sprintf("Parameter for %v\n", funcDecl.Name.String())
						v.contexts = append(v.contexts, &contextInfo{
							ident:   contextAt,
							foundAt: foundAt,
						})
					}

					//funcDeclT := v.info.ObjectOf(funcDecl.Name)
					//fmt.Printf("funcDecl: %v \n", funcDeclT)

					//printParamsAndBody(funcDecl.Type.Params.List, funcDecl.Body.List)

				case *ast.GenDecl:
					// Entering new scope, reset context records
					v.contexts = []*contextInfo{}

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

					// To print out the function signature
					//genDeclT := v.info.TypeOf(val)
					//fmt.Printf("genDecl: %v %v\n", genDeclValueSpec.Names[0], genDeclT.Underlying())

					contextAtParams := v.hasContextParam(funcLit.Type.Params.List)
					var buf bytes.Buffer
					printer.Fprint(&buf, v.tFSet, node)
					genDeclStr := buf.String()

					for _, contextAt := range contextAtParams {
						v.contexts = append(v.contexts, &contextInfo{
							ident:   contextAt,
							foundAt: genDeclStr,
						})
					}
					//printParamsAndBody(funcLit.Type.Params.List, funcLit.Body.List)
				}
			}

		case ast.Stmt:
			astNode := node.(ast.Stmt)
			// Only interested in AssignStmt
			assignStmt, ok := astNode.(*ast.AssignStmt)
			if !ok {
				break
			}

			var buf bytes.Buffer
			printer.Fprint(&buf, v.tFSet, node)
			assignStmtStr := buf.String()

			//fmt.Printf(" Processing AssignStmt: %v\n", assignStmtStr)
			//fmt.Printf("  Processing RHS\n")
			v.assignStmtRHS(node, assignStmt.Rhs)

			//fmt.Printf("  Processing LHS\n")
			contextAtLhs := v.assignStmtLHS(assignStmt.Lhs)
			for _, contextIdx := range contextAtLhs {
				contextIdent := assignStmt.Lhs[contextIdx].(*ast.Ident)

				v.contexts = append(v.contexts, &contextInfo{
					ident:   contextIdent,
					foundAt: assignStmtStr,
				})
			}
		}
	}
	return v
}

// Process LHS to see if a new context is created
// returns the location of context in the expr list
func (v *PrintASTVisitor) assignStmtLHS(assignStmtLHS []ast.Expr) []int {
	contextAt := []int{}

	for idx, lhs := range assignStmtLHS {
		lhsIdent, ok := lhs.(*ast.Ident)
		// context should not be embedded
		// IMPROVE: Maybe allow it to be
		if !ok {
			continue
		}

		matched := v.isNetContextType(lhsIdent)
		if matched {
			contextAt = append(contextAt, idx)
		}
	}

	return contextAt
}

// Process RHS to see if context is used
func (v *PrintASTVisitor) assignStmtRHS(node ast.Node, assignStmtRHS []ast.Expr) {
	// Only interested in Func calls
	assignCallExpr, ok := assignStmtRHS[0].(*ast.CallExpr)
	if !ok {
		return
	}

	v.hasContextArg(node, assignCallExpr.Args)
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
			contexts = append(contexts, xIdent)
			// selIdent := selectorExpr.Sel
			// fmt.Printf(" Context Param Type: %v %v\n", p.Names[0], selIdent)
		}
	}

	return contexts
}

// Check if a context type is part of the argument to the function call
// TODO: Handle AssignStmt can be nested inside another AssignStmt
func (v *PrintASTVisitor) hasContextArg(node ast.Node, args []ast.Expr) {
	for idx, a := range args {
		var ident *ast.Ident
		switch a.(type) {
		case *ast.CallExpr:
			aCallExpr := a.(*ast.CallExpr)

			selectorExpr, _ := aCallExpr.Fun.(*ast.SelectorExpr)

			ident = selectorExpr.X.(*ast.Ident)
		case *ast.Ident:
			ident = a.(*ast.Ident)
		default:
			continue
		}

		matched := v.isNetContextType(ident)
		if matched {
			var buf bytes.Buffer
			printer.Fprint(&buf, v.tFSet, node)
			nodeStr := buf.String()

			fmt.Printf("Found other context values that can be used for %#v.\n", nodeStr)
			v.PrintPossibleContext()

			reader := bufio.NewReader(os.Stdin)
			fmt.Print("  Replace this context? (y/n)\n  => ")
			text, _ := reader.ReadString('\n')

			if text == "y\n" {
				fmt.Print("  Which context to replace with?\n  => ")
				text, err := reader.ReadString('\n')
				if err != nil {
					fmt.Printf("  error when trying to process input: %v\n", err)
					os.Exit(1)
				}

				text = strings.Replace(text, "\n", "", -1)
				repIdx, err := strconv.ParseInt(text, 10, 0)
				if err != nil {
					fmt.Println("  error when converting %v to number. err: %v", text, err)
					os.Exit(1)
				}
				if repIdx < 0 || int(repIdx) >= len(v.contexts) {
					fmt.Printf("  Invalid replacement index of %v provided\n", idx)
					os.Exit(1)
				}

				fmt.Printf("Len: %v\n", len(v.contexts))
				//TODO: Just replacing the args like this will result in <args>,\n being printed
				//      instead of just <arg>). Need to find out why and do it properly
				args[idx] = v.contexts[repIdx].ident
			}
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
