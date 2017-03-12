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
	}
}

type PrintASTVisitor struct {
	tFSet *token.FileSet
	astf  *ast.File
	info  *types.Info
}

func (v *PrintASTVisitor) Visit(node ast.Node) ast.Visitor {
	if node != nil {
		//fmt.Printf("%v : %s", node, reflect.TypeOf(node).String())
		switch node.(type) {
		case ast.Expr:
			t := v.info.TypeOf(node.(ast.Expr))
			//if t != nil {
			//	fmt.Printf(" : %s", t.String())
			//}

			if t != nil && t.String() == "github.com/myteksi/go/vendor/golang.org/x/net/context.Context" {
				//position := v.tFSet.Position(node.Pos())
				//fmt.Printf("%v:%v : %v : %s", position.Line, position.Offset, node, reflect.TypeOf(node).String())
				//fmt.Printf(" : %s", t.String())
				//fmt.Println()
			}
		case ast.Decl:
			declNode := node.(ast.Decl)
			if declNode != nil {
				// For printing out the function and the body of a normal function declaration
				//funcDeclNode, ok := declNode.(*ast.FuncDecl)
				//if ok {
				//fmt.Printf("funcDecl: %v \n", funcDeclNode)
				//for _, body := range funcDeclNode.Body.List {
				//fmt.Printf("  funcBody: %v \n", body)

				//}
				//}

				genDeclNode, ok := declNode.(*ast.GenDecl)
				if ok && genDeclNode.Tok == token.VAR {
					position := v.tFSet.Position(node.Pos())
					fmt.Printf("genDecl: %v:%v : %v \n", position.Line, position.Offset, genDeclNode)

					for _, v1 := range genDeclNode.Specs {
						for _, v2 := range v1.(*ast.ValueSpec).Values {
							// To print out the function signature
							genDeclT := v.info.TypeOf(v2)
							fmt.Printf("  genDeclExpr: %v\n", genDeclT)

							// To print out body of a variable function type function
							funcLit, ok := v2.(*ast.FuncLit)
							if ok {
								for _, body := range funcLit.Body.List {
									fmt.Printf("    genDeclFuncBody: %v\n", body)
								}
							}
							// To print out type of each param of the function signature
							//signatureType, ok := t.Underlying().(*types.Signature)
							//if ok {
							//	tuples := signatureType.Params()
							//	tuplesLen := tuples.Len()
							//	for i := 0; i < tuplesLen; i++ {
							//		fmt.Printf("    params: %v \n", tuples.At(i))
							//	}
						}
					}
				}
			}
		case ast.Stmt:
			//astNode := node.(ast.Stmt)
			//if astNode != nil {
			//assignStmt, ok := astNode.(*ast.AssignStmt)
			//if ok {
			//fmt.Printf("assign: %v\n", assignStmt)
			//}
			//}
		}
		//fmt.Println()
	}
	return v
}

//func main() {
//
//	path := "/Users/ryanlaw/go/src/github.com/myteksi/go/dispatcher/grab-id/logic/login/google.go"
//
//	fset := token.NewFileSet() // positions are relative to fset
//	f, err := parser.ParseFile(fset, path, nil, 0)
//	if err != nil {
//		panic(err)
//	}
//
//	ast.Inspect(f, func(n ast.Node) bool {
//		var s string
//		switch x := n.(type) {
//		case *ast.BasicLit:
//			s = x.Value
//		case *ast.Ident:
//			s = x.Name
//		}
//		if s != "" {
//			fmt.Printf("%s:\t%s\n", fset.Position(n.Pos()), s)
//		}
//
//		return true
//	})
//}
