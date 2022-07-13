package main

import (
	"fmt"
	"io/fs"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/urfave/cli/v2"
)

func main() {
	err := app.Run(os.Args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

var (
	app = cli.App{
		Name:  "nbe",
		Usage: "CLI for using the NATS by Example repo.",
		Commands: []*cli.Command{
			&runCmd,
			&buildCmd,
			&serveCmd,
			&generateCmd,
		},
	}

	runCmd = cli.Command{
		Name:  "run",
		Usage: "Run an example using containers.",
		Description: `To run an example, the current requirement is to clone the
repo and run the command in the root the repo.

Future versions may leverage pre-built images hosted in a registry to reduce
the runtime.`,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "cluster",
				Usage: "Use compose file with a NATS cluster.",
				Value: false,
			},
			&cli.StringFlag{
				Name:  "name",
				Usage: "Explicit name of the run. This maps to the Compose project name and image tag.",
				Value: "",
			},
		},
		Action: func(c *cli.Context) error {
			cluster := c.Bool("cluster")
			repo := c.String("repo")
			name := c.String("name")
			example := c.Args().First()

			repo, err := os.Getwd()
			if err != nil {
				return err
			}

			r := ExampleRunner{
				Name:    name,
				Repo:    repo,
				Example: example,
				Cluster: cluster,
			}

			return r.Run()
		},
	}

	serveCmd = cli.Command{
		Name:  "serve",
		Usage: "Dev server for the docs.",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "dir",
				Usage: "Directory containing the rendered HTML.",
				Value: "html",
			},
			&cli.StringFlag{
				Name:  "addr",
				Usage: "HTTP bind address.",
				Value: "localhost:8000",
			},
		},
		Action: func(c *cli.Context) error {
			addr := c.String("addr")
			dir := c.String("dir")
			return http.ListenAndServe(addr, http.FileServer(http.Dir(dir)))
		},
	}

	generateCmd = cli.Command{
		Name:  "generate",
		Usage: "Set of commands for generating various files from examples.",
		Subcommands: []*cli.Command{
			&generateOutputCmd,
		},
	}

	generateOutputCmd = cli.Command{
		Name:  "output",
		Usage: "Generate execution output for examples.",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "source",
				Usage: "Source directory containing the examples.",
				Value: "examples",
			},
			&cli.BoolFlag{
				Name:  "recreate",
				Usage: "If true, recreate all previously generated output files.",
			},
		},
		Action: func(c *cli.Context) error {
			repo, err := os.Getwd()
			if err != nil {
				return err
			}

			source := c.String("source")
			recreate := c.Bool("recreate")

			root, err := parseExamples(source)
			if err != nil {
				return err
			}

			// Enumerate all the example implementations.
			for _, c := range root.Categories {
				for _, e := range c.Examples {
					for _, i := range e.Implementations {
						if err := generateOutput(repo, i.Path, recreate); err != nil {
							log.Printf("%s: %s", i.Path, err)
						}
					}
				}
			}

			return nil
		},
	}

	buildCmd = cli.Command{
		Name:  "build",
		Usage: "Takes the examples and builds documentation from it.",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "source",
				Usage: "Source directory containing the examples.",
				Value: "examples",
			},
			&cli.StringFlag{
				Name:  "static",
				Usage: "Directory containing static files that will be copied in.",
				Value: "static",
			},
			&cli.StringFlag{
				Name:  "output",
				Usage: "Directory the HTML files will be written to. Note, this will delete the existing directory if present.",
				Value: "html",
			},
		},
		Action: func(c *cli.Context) error {
			source := c.String("source")
			output := c.String("output")
			static := c.String("static")

			root, err := parseExamples(source)
			if err != nil {
				return err
			}

			os.RemoveAll(output)
			os.MkdirAll(output, 0755)

			entries, err := fs.ReadDir(os.DirFS(static), ".")
			if err != nil {
				return err
			}

			for _, e := range entries {
				b, err := ioutil.ReadFile(filepath.Join(static, e.Name()))
				if err != nil {
					return err
				}
				err = createFile(filepath.Join(output, e.Name()), b)
				if err != nil {
					return err
				}
			}

			return generateDocs(root, output)
		},
	}
)