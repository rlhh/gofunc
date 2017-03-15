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
	contexts []*ast.Ident
}

func (v *PrintASTVisitor) Visit(node ast.Node) ast.Visitor {
	if node != nil {
		switch node.(type) {
		case ast.Decl:
			declNode := node.(ast.Decl)
			if declNode != nil {
				switch declNode.(type) {
				case *ast.FuncDecl:
					funcDecl := declNode.(*ast.FuncDecl)

					v.hasContextParam(funcDecl.Type.Params.List)

					//funcDeclT := v.info.ObjectOf(funcDecl.Name)
					//fmt.Printf("funcDecl: %v \n", funcDeclT)

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

					// To print out the function signature
					//genDeclT := v.info.TypeOf(val)
					//fmt.Printf("genDecl: %v %v\n", genDeclValueSpec.Names[0], genDeclT.Underlying())

					v.hasContextParam(funcLit.Type.Params.List)
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

			fmt.Printf(" Processing AssignStmt: %v\n", assignStmt)
			//fmt.Printf("  Processing RHS\n")
			v.assignStmtRHS(assignStmt.Rhs)

			//fmt.Printf("  Processing LHS\n")
			v.assignStmtLHS(assignStmt.Lhs)
		}
	}
	return v
}

// Process LHS to see if a new context is created
func (v *PrintASTVisitor) assignStmtLHS(assignStmtLHS []ast.Expr) {
	for _, lhs := range assignStmtLHS {
		lhsIdent, ok := lhs.(*ast.Ident)
		// context should not be embedded
		// IMPROVE: Maybe allow it to be
		if !ok {
			continue
		}

		matched := v.isNetContextType(lhsIdent)
		if matched {
			v.contexts = append(v.contexts, lhsIdent)
		}
	}
}

// Process RHS to see if context is used
func (v *PrintASTVisitor) assignStmtRHS(assignStmtRHS []ast.Expr) {
	// Only interested in Func calls
	assignCallExpr, ok := assignStmtRHS[0].(*ast.CallExpr)
	if !ok {
		return
	}

	v.hasContextArg(assignCallExpr.Args)
}

// Check if a context.Context is passed in as a param
// This only works properly (has been tested) when there's only one single variable defined
//   Works: ctx context.Context
//   Does Not Work: ctx1, ctx2 context.Context
func (v *PrintASTVisitor) hasContextParam(params []*ast.Field) {
	v.contexts = []*ast.Ident{}
	for _, p := range params {
		selectorExpr, ok := p.Type.(*ast.SelectorExpr)
		// All context params are selectorExpr
		if !ok {
			continue
		}

		xIdent := selectorExpr.X.(*ast.Ident)
		matched := v.isNetContextType(xIdent)
		if matched {
			v.contexts = append(v.contexts, p.Names[0])

			// selIdent := selectorExpr.Sel
			// fmt.Printf(" Context Param Type: %v %v\n", p.Names[0], selIdent)
		}
	}
}

// Check if a context type is part of the argument to the function call
// TODO: Handle AssignStmt can be nested inside another AssignStmt
func (v *PrintASTVisitor) hasContextArg(args []ast.Expr) {
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
		fmt.Printf(" ident: %v\n", ident)

		matched := v.isNetContextType(ident)
		if matched {
			// TODO: Print out the whole line, otherwise it's hard to tell
			fmt.Printf("  Found other Context values in the same scope as %#v. Other possible contexts %v\n", ident, v.contexts)

			reader := bufio.NewReader(os.Stdin)
			fmt.Print("Replace current context with earlier context? (y/n)\n  => ")
			text, _ := reader.ReadString('\n')

			if text == "y\n" {
				//TODO: Allow users to choose which context to change to
				//TODO: Just replacing the args like this will result in <args>,\n being printed
				//      instead of just <arg>). Need to find out why and do it properly
				args[idx] = v.contexts[len(v.contexts)-1]
			}
		}
	}
}

func (v *PrintASTVisitor) isNetContextType(ident *ast.Ident) bool {
	identInfo := v.info.ObjectOf(ident)

	var pkgStr string
	switch identInfo.(type) {
	case *types.PkgName:
		contextPkg := identInfo.Parent().Lookup("context")
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
