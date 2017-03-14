package main

//credits: https://gist.github.com/cxwangyi/e1887879dcaa750e5469

import (
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
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

		//var buf bytes.Buffer
		//printer.Fprint(&buf, fset, f)
		//fmt.Println(buf.String())
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
		case ast.Expr:
			//exprNode := node.(ast.Expr)
			//t := v.info.TypeOf(exprNode)
			//if t != nil && t.String() == "github.com/myteksi/go/vendor/golang.org/x/net/context.Context" {
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
			//}
		case ast.Decl:
			declNode := node.(ast.Decl)
			if declNode != nil {
				switch declNode.(type) {
				case *ast.FuncDecl:
					funcDecl := declNode.(*ast.FuncDecl)

					funcDeclT := v.info.ObjectOf(funcDecl.Name)
					fmt.Printf("funcDecl: %v \n", funcDeclT)

					v.hasContextParam(funcDecl.Type.Params.List)
					printParamsAndBody(funcDecl.Type.Params.List, funcDecl.Body.List)

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

			fmt.Printf(" Processing AssignStmt: %v\n", assignStmt)
			fmt.Printf("  Processing LHS\n")
			v.assignStmtLHS(assignStmt.Lhs)
			fmt.Printf("  Processing RHS\n")
			v.assignStmtRHS(assignStmt.Rhs)
		}
	}
	return v
}

// Process LHS to see if a new context is created
func (v *PrintASTVisitor) assignStmtLHS(assignStmtLHS []ast.Expr) {
	for _, lhs := range assignStmtLHS {
		lhsIdent := lhs.(*ast.Ident)

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
		selectorExpr := p.Type.(*ast.SelectorExpr)
		xIdent := selectorExpr.X.(*ast.Ident)
		selIdent := selectorExpr.Sel

		matched := v.isNetContextType(xIdent)
		if matched {
			v.contexts = append(v.contexts, p.Names[0])
			fmt.Printf(" Context Param Type: %v %v\n", p.Names[0], selIdent)
		}
	}
}

// Check if a context type is part of the argument to the function call
// TODO: Handle AssignStmt can be nested inside another AssignStmt
func (v *PrintASTVisitor) hasContextArg(args []ast.Expr) {
	for _, a := range args {

		var selectorExpr *ast.SelectorExpr
		switch a.(type) {
		case *ast.CallExpr:
			aCallExpr := a.(*ast.CallExpr)

			selectorExpr = aCallExpr.Fun.(*ast.SelectorExpr)
		case *ast.Ident:
			aIdent := a.(*ast.Ident)

			switch aIdent.Obj.Decl.(type) {
			case *ast.Field:
				aFieldType := aIdent.Obj.Decl.(*ast.Field).Type

				selectorExpr = aFieldType.(*ast.SelectorExpr)
			case *ast.AssignStmt:
				// This can happen when Go inlines code for cases like
				//  childCtx := context.WithValue(ctx, "abc", 123)
				//  err := VerifyRecoveryToken(childCtx)
				// gets converted something like
				//  err := VerifyRecoveryToken(childCtx := context.WithValue(ctx, "abc", 123))
				// in the AST
				//
				// We assume that this is procssed in the Visit loop
				continue
			default:
				fmt.Printf("unhandled hasContextArg check in *ast.Ident: Type: %#v\n", a)
				continue
			}

		default:
			continue
		}

		xIdent := selectorExpr.X.(*ast.Ident)
		selIdent := selectorExpr.Sel

		matched := v.isNetContextType(xIdent)
		if matched {
			fmt.Printf("  Possible Parent ctx: %v\n", v.contexts)
			fmt.Printf("  Context Arg Type: %v\n", selIdent)

			// Only perform replacement if this is a context.Background()
			//if selIdent.Name == "Background" {
			//	args[idx] = v.contexts[len(v.contexts)-1]
			//}
		}
	}
}

func (v *PrintASTVisitor) isNetContextType(ident *ast.Ident) bool {
	identInfo := v.info.ObjectOf(ident)

	var matched bool
	switch identInfo.(type) {
	case *types.PkgName:
		contextPkg := identInfo.Parent().Lookup("context")
		contextPkgName := contextPkg.(*types.PkgName)

		matched, _ = regexp.MatchString("golang.org/x/net/context", contextPkgName.Imported().Path())
	case *types.Var:
		def := v.info.Defs[ident]

		// A plain string check is not ideal but it is the easiest
		matched, _ = regexp.MatchString("golang.org/x/net/context", def.Type().String())
	}
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
