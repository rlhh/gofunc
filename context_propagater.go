package main

//credits: https://gist.github.com/cxwangyi/e1887879dcaa750e5469

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/printer"
	"go/token"
	"go/types"
	"log"
	"regexp"
)

func main() {
	path := "/Users/ryanlaw/go/src/github.com/myteksi/go/dispatcher/grab-id/logic/recovery"

	fset := token.NewFileSet()

	pkgs, e := parser.ParseDir(fset, path, nil, 0)
	if e != nil {
		log.Fatal(e)
		return
	}

	astf := make([]*ast.File, 0)
	for _, pkg := range pkgs {
		fmt.Printf("package %v\n", pkg.Name)
		for _, f := range pkg.Files {
			//fmt.Printf("file %v\n", fp)
			astf = append(astf, f)
		}
	}

	config := &types.Config{
		Error: func(err error) {
			fmt.Printf("Type check error: %v\n", err)
		},
		Importer: importer.Default(),
	}
	info := types.Info{
		Types: make(map[ast.Expr]types.TypeAndValue),
		Defs:  make(map[*ast.Ident]types.Object),
		Uses:  make(map[*ast.Ident]types.Object),
	}
	pkg, err := config.Check(path, fset, astf, &info)
	if e != nil {
		fmt.Printf("types.Config.Check error: %v\n", err)
	}
	fmt.Printf("types.Config.Check got %v\n", pkg.String())

	for _, f := range astf {
		ast.Walk(&PrintASTVisitor{tFSet: fset, astf: f, info: &info}, f)

		var buf bytes.Buffer
		printer.Fprint(&buf, fset, f)
		fmt.Println(buf.String())
		//ast.Print(fset, rewritten)
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
		case ast.Expr:
			exprNode := node.(ast.Expr)
			t := v.info.TypeOf(exprNode)
			if t != nil && t.String() == "github.com/myteksi/go/vendor/golang.org/x/net/context.Context" {
				//position := v.tFSet.Position(node.Pos())
				//fmt.Printf("%v:%v : %v : %s", position.Line, position.Offset, exprNode, reflect.TypeOf(node).String())
				//fmt.Printf(" : %s", t.String())
				//fmt.Println()

				// prints out context.Background()
				//callExpr, ok := exprNode.(*ast.CallExpr)
				//if ok {
				//fmt.Printf("  lastSeenContext: %v \n", v.contexts)
				//fmt.Printf("  callExpr: %v %v \n", callExpr.Fun, callExpr.Args)
				//	node = v.contexts[len(v.contexts)-1]
				//}

				//identCtx, ok := node.(*ast.Ident)
				//if ok {
				//fmt.Printf(" LastCtx: %v: %v\n", position.Line, position.Offset)
				//v.contexts = append(v.contexts, identCtx)
				//}
			}
		case ast.Decl:
			declNode := node.(ast.Decl)
			if declNode != nil {
				switch declNode.(type) {
				case *ast.FuncDecl:
					funcDecl := declNode.(*ast.FuncDecl)

					funcDeclT := v.info.ObjectOf(funcDecl.Name)
					fmt.Printf("funcDecl: %v \n", funcDeclT)

					printParamsAndBody(funcDecl.Type.Params.List, funcDecl.Body.List)
					v.hasContextParam(funcDecl.Type.Params.List)

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
					genDeclT := v.info.TypeOf(val)
					fmt.Printf("genDecl: %v %v\n", genDeclValueSpec.Names[0], genDeclT.Underlying())

					v.hasContextParam(funcLit.Type.Params.List)
					printParamsAndBody(funcLit.Type.Params.List, funcLit.Body.List)
				}
			}
		case ast.Stmt:
			astNode := node.(ast.Stmt)
			// Only interested in AssignStmt
			assignStmt, ok := astNode.(*ast.AssignStmt)
			if !ok {
				break
			}

			// Only interested in Func calls
			assignCallExpr, ok := assignStmt.Rhs[0].(*ast.CallExpr)
			if !ok {
				break
			}

			v.hasContextArg(assignCallExpr.Args)
		}
	}
	return v
}

// Check if a context.Context is passed in as a param
func (v *PrintASTVisitor) hasContextParam(params []*ast.Field) {
	for _, p := range params {
		selectorExpr := p.Type.(*ast.SelectorExpr)
		xIdent := selectorExpr.X.(*ast.Ident)
		selIdent := selectorExpr.Sel

		matched := v.isNetContextType(xIdent)
		if matched {
			v.contexts = append(v.contexts, xIdent)
			fmt.Printf(" Context Param Type: %v %v\n", p.Names, selIdent)
		}
	}
}

// Check if context.* is part of the argument to the function call
// This only checks for selector type context arg (context.Background(), context.WithValue(...))
func (v *PrintASTVisitor) hasContextArg(args []ast.Expr) {
	for _, a := range args {
		aCallExpr, ok := a.(*ast.CallExpr)

		// This can be *ast.Ident, not *ast.CallExpr
		if !ok {
			continue
		}

		selectorExpr := aCallExpr.Fun.(*ast.SelectorExpr)
		xIdent := selectorExpr.X.(*ast.Ident)
		selIdent := selectorExpr.Sel

		matched := v.isNetContextType(xIdent)
		if matched {

			fmt.Printf("\n Possible Parent ctx %v\n", v.contexts)
			fmt.Printf(" Context Arg Type: %v\n", selIdent)
		}
	}
}

func (v *PrintASTVisitor) isNetContextType(xIdent *ast.Ident) bool {
	xIdentInfo := v.info.ObjectOf(xIdent)
	contextPkg := xIdentInfo.Parent().Lookup("context")
	contextPkgName := contextPkg.(*types.PkgName)

	matched, _ := regexp.MatchString("golang.org/x/net/context", contextPkgName.Imported().Path())

	return matched
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
