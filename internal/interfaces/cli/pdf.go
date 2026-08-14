package cli

import (
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"membox"
)

const pdfMediaType = "application/pdf"

func newPDFCommand(runtime *runtime) *cobra.Command {
	pdf := parentCommand("pdf", "Import and manage PDF documents", "a pdf command is required")
	pdf.AddCommand(
		newPDFImportCommand(runtime), newPDFListCommand(runtime), newPDFSearchCommand(runtime),
		newPDFShowCommand(runtime), newPDFUpdateCommand(runtime), newPDFOpenCommand(runtime),
		newPDFRenameCommand(runtime), newPDFDeleteCommand(runtime), newPDFRestoreCommand(runtime),
		newPDFConvertCommand(runtime), newPDFServerCommand(runtime),
	)
	return pdf
}

func newPDFImportCommand(runtime *runtime) *cobra.Command {
	var destination, title, authors, keywords string
	var year int
	var openAfter, jsonOutput bool
	command := &cobra.Command{Use: "import <file.pdf>", Short: "Copy a PDF into the managed PDF directory and index it", Args: exactArgs(1, "PDF path")}
	command.Flags().StringVar(&destination, "to", "", "PDF directory (default: ~/Documents/membox-pdfs)")
	command.Flags().StringVar(&title, "title", "", "searchable title override")
	command.Flags().StringVar(&authors, "authors", "", "searchable authors")
	command.Flags().IntVar(&year, "year", 0, "publication year")
	command.Flags().StringVar(&keywords, "keywords", "", "searchable keywords")
	command.Flags().BoolVar(&openAfter, "open", false, "open the imported PDF")
	command.Flags().BoolVar(&jsonOutput, "json", false, "output JSON")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		box, err := runtime.get()
		if err != nil {
			return err
		}
		result, err := box.ImportPDF(cmd.Context(), membox.ImportPDFCommand{
			SourcePath: args[0], DestinationRoot: destination, Title: title, Authors: authors, Year: year, Keywords: keywords,
		})
		if err != nil {
			return err
		}
		if jsonOutput {
			if err := writeJSON(cmd, result); err != nil {
				return err
			}
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "Imported PDF %s: %s\n", result.Document.ID, result.Path)
		}
		if !openAfter {
			return nil
		}
		opener, err := runtime.launcher.OpenCommand(cmd.Context(), result.Path)
		if err != nil {
			return err
		}
		if err := opener.Run(); err != nil {
			return fmt.Errorf("opening PDF: %w", err)
		}
		return nil
	}
	return command
}

func newPDFListCommand(runtime *runtime) *cobra.Command {
	var limit int
	var all, jsonOutput bool
	command := &cobra.Command{Use: "list", Short: "List indexed PDFs", Args: noArgs}
	command.Flags().IntVar(&limit, "limit", 100, "maximum PDFs")
	command.Flags().BoolVar(&all, "all", false, "include missing and untracked PDFs")
	command.Flags().BoolVar(&jsonOutput, "json", false, "output JSON")
	command.RunE = func(cmd *cobra.Command, _ []string) error {
		box, err := runtime.get()
		if err != nil {
			return err
		}
		documents, err := box.ListDocuments(cmd.Context(), membox.ListDocumentsQuery{Limit: limit, All: all, MediaType: pdfMediaType})
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(cmd, documents)
		}
		if len(documents) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No PDFs.")
			return nil
		}
		writer := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		fmt.Fprintln(writer, "ID\tTITLE\tAUTHORS\tYEAR\tPAGES\tPATH")
		for _, document := range documents {
			fmt.Fprintf(writer, "%s\t%s\t%s\t%d\t%d\t%s\n", shortID(document.ID), document.Title, document.Authors, document.Year, document.PageCount, document.Path)
		}
		return writer.Flush()
	}
	return command
}

func newPDFSearchCommand(runtime *runtime) *cobra.Command {
	var limit int
	var jsonOutput bool
	command := &cobra.Command{Use: "search <query>", Short: "Search PDF metadata and extracted text", Args: exactArgs(1, "query")}
	command.Flags().IntVar(&limit, "limit", 20, "maximum results")
	command.Flags().BoolVar(&jsonOutput, "json", false, "output JSON")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		box, err := runtime.get()
		if err != nil {
			return err
		}
		results, err := box.SearchDocuments(cmd.Context(), membox.SearchDocumentsQuery{Query: args[0], Limit: limit, MediaType: pdfMediaType})
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(cmd, results)
		}
		if len(results) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No results.")
			return nil
		}
		for _, result := range results {
			fmt.Fprintf(cmd.OutOrStdout(), "%s  %s\n%s\n", shortID(result.DocumentID), result.Title, result.Path)
			if result.Snippet != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "%s\n", result.Snippet)
			}
			fmt.Fprintln(cmd.OutOrStdout())
		}
		return nil
	}
	return command
}

func newPDFConvertCommand(runtime *runtime) *cobra.Command {
	var server string
	var jsonOutput bool
	command := &cobra.Command{Use: "convert <document-id>", Short: "Upload a PDF to the configured converter and publish its Markdown", Args: exactArgs(1, "document ID")}
	command.Flags().StringVar(&server, "server", "", "one-off converter URL override")
	command.Flags().BoolVar(&jsonOutput, "json", false, "output JSON")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		box, _, err := requirePDF(cmd, runtime, args[0])
		if err != nil {
			return err
		}
		result, err := box.ConvertPDF(cmd.Context(), membox.ConvertPDFCommand{Selector: args[0], ServerURL: server})
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(cmd, result)
		}
		verb := "Updated"
		if result.Created {
			verb = "Created"
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s Markdown index %s from PDF %s: %s\n", verb, result.MarkdownDocument.ID, result.SourceDocumentID, result.MarkdownPath)
		if len(result.Chapters) != 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "Published %d linked chapter document(s).\n", len(result.Chapters))
		}
		return nil
	}
	return command
}

func newPDFServerCommand(runtime *runtime) *cobra.Command {
	var jsonOutput bool
	command := &cobra.Command{Use: "server [url]", Short: "Show or set the optional PDF converter server", Args: func(cmd *cobra.Command, args []string) error {
		if len(args) > 1 {
			return usageErr(cmd, "expected at most one server URL")
		}
		return nil
	}}
	command.Flags().BoolVar(&jsonOutput, "json", false, "output JSON")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		box, err := runtime.get()
		if err != nil {
			return err
		}
		var config membox.PDFConverterConfigView
		if len(args) == 1 {
			config, err = box.SetPDFConverterServer(cmd.Context(), args[0])
		} else {
			config, err = box.GetPDFConverterConfig(cmd.Context())
		}
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(cmd, config)
		}
		if config.ServerURL == "" {
			fmt.Fprintf(cmd.OutOrStdout(), "PDF converter server is not configured (%s)\n", config.ConfigPath)
			return nil
		}
		fmt.Fprintf(cmd.OutOrStdout(), "PDF converter server: %s\n", config.ServerURL)
		return nil
	}
	return command
}

func requirePDF(cmd *cobra.Command, runtime *runtime, selector string) (*membox.Box, membox.DocumentView, error) {
	box, err := runtime.get()
	if err != nil {
		return nil, membox.DocumentView{}, err
	}
	document, err := box.GetDocument(cmd.Context(), membox.GetDocumentQuery{Selector: selector})
	if err != nil {
		return nil, membox.DocumentView{}, err
	}
	if document.MediaType != pdfMediaType {
		return nil, membox.DocumentView{}, fmt.Errorf("document %s is %s, not a PDF", document.ID, document.MediaType)
	}
	return box, document, nil
}

func newPDFShowCommand(runtime *runtime) *cobra.Command {
	var jsonOutput bool
	command := &cobra.Command{Use: "show <document-id>", Short: "Show PDF metadata and identity", Args: exactArgs(1, "document ID")}
	command.Flags().BoolVar(&jsonOutput, "json", false, "output JSON")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		_, document, err := requirePDF(cmd, runtime, args[0])
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(cmd, document)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "ID:          %s\n", document.ID)
		fmt.Fprintf(cmd.OutOrStdout(), "Path:        %s\n", document.Path)
		fmt.Fprintf(cmd.OutOrStdout(), "Status:      %s\n", document.Status)
		fmt.Fprintf(cmd.OutOrStdout(), "Title:       %s\n", document.Title)
		fmt.Fprintf(cmd.OutOrStdout(), "Authors:     %s\n", document.Authors)
		fmt.Fprintf(cmd.OutOrStdout(), "Year:        %d\n", document.Year)
		fmt.Fprintf(cmd.OutOrStdout(), "Keywords:    %s\n", document.Keywords)
		fmt.Fprintf(cmd.OutOrStdout(), "Pages:       %d\n", document.PageCount)
		fmt.Fprintf(cmd.OutOrStdout(), "Modified at: %s\n", document.UpdatedAt.Format(time.RFC3339))
		fmt.Fprintf(cmd.OutOrStdout(), "Size:        %d\n", document.Size)
		fmt.Fprintf(cmd.OutOrStdout(), "SHA-256:     %s\n", document.SHA256)
		return nil
	}
	return command
}

func newPDFUpdateCommand(runtime *runtime) *cobra.Command {
	var title, authors, keywords string
	var year int
	var jsonOutput bool
	command := &cobra.Command{Use: "update <document-id>", Short: "Update searchable PDF metadata without rewriting the PDF", Args: exactArgs(1, "document ID")}
	command.Flags().StringVar(&title, "title", "", "title (empty clears to filename)")
	command.Flags().StringVar(&authors, "authors", "", "authors (empty clears)")
	command.Flags().IntVar(&year, "year", 0, "publication year (0 clears)")
	command.Flags().StringVar(&keywords, "keywords", "", "keywords (empty clears)")
	command.Flags().BoolVar(&jsonOutput, "json", false, "output JSON")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		box, _, err := requirePDF(cmd, runtime, args[0])
		if err != nil {
			return err
		}
		var patch membox.UpdatePDFMetadataCommand
		patch.Selector = args[0]
		if cmd.Flags().Changed("title") {
			patch.Title = &title
		}
		if cmd.Flags().Changed("authors") {
			patch.Authors = &authors
		}
		if cmd.Flags().Changed("year") {
			patch.Year = &year
		}
		if cmd.Flags().Changed("keywords") {
			patch.Keywords = &keywords
		}
		document, err := box.UpdatePDFMetadata(cmd.Context(), patch)
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(cmd, document)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Updated PDF %s\n", document.ID)
		return nil
	}
	return command
}

func newPDFOpenCommand(runtime *runtime) *cobra.Command {
	command := &cobra.Command{Use: "open <document-id>", Short: "Open a PDF with the system open command", Args: exactArgs(1, "document ID")}
	command.RunE = func(cmd *cobra.Command, args []string) error {
		_, document, err := requirePDF(cmd, runtime, args[0])
		if err != nil {
			return err
		}
		if document.Status != "active" {
			return fmt.Errorf("document %s is %s at %s", document.ID, document.Status, document.Path)
		}
		opener, err := runtime.launcher.OpenCommand(cmd.Context(), document.Path)
		if err != nil {
			return err
		}
		if err := opener.Run(); err != nil {
			return fmt.Errorf("opening PDF: %w", err)
		}
		return nil
	}
	return command
}

func newPDFRenameCommand(runtime *runtime) *cobra.Command {
	var title string
	var jsonOutput bool
	command := &cobra.Command{Use: "rename <document-id> <new-filename.pdf>", Short: "Rename a PDF without changing its UUID", Args: exactArgs(2, "document ID and new filename")}
	command.Flags().StringVar(&title, "title", "", "also update the searchable title")
	command.Flags().BoolVar(&jsonOutput, "json", false, "output JSON")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		box, _, err := requirePDF(cmd, runtime, args[0])
		if err != nil {
			return err
		}
		result, err := box.RenameDocument(cmd.Context(), membox.RenameDocumentCommand{Selector: args[0], NewFilename: args[1], Title: title})
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(cmd, result)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Renamed PDF %s: %s\n", result.DocumentID, result.Path)
		return nil
	}
	return command
}

func newPDFDeleteCommand(runtime *runtime) *cobra.Command {
	var jsonOutput bool
	command := &cobra.Command{Use: "delete <document-id>", Short: "Move a PDF to the membox trash", Args: exactArgs(1, "document ID")}
	command.Flags().BoolVar(&jsonOutput, "json", false, "output JSON")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		box, _, err := requirePDF(cmd, runtime, args[0])
		if err != nil {
			return err
		}
		result, err := box.DeleteDocument(cmd.Context(), membox.DeleteDocumentCommand{Selector: args[0]})
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(cmd, result)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Moved PDF %s to trash: %s\n", result.DocumentID, result.Path)
		return nil
	}
	return command
}

func newPDFRestoreCommand(runtime *runtime) *cobra.Command {
	var jsonOutput bool
	command := &cobra.Command{Use: "restore <document-id>", Short: "Restore a PDF from the membox trash", Args: exactArgs(1, "document ID")}
	command.Flags().BoolVar(&jsonOutput, "json", false, "output JSON")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		box, _, err := requirePDF(cmd, runtime, args[0])
		if err != nil {
			return err
		}
		result, err := box.RestoreTrashedDocument(cmd.Context(), membox.RestoreDocumentCommand{Selector: args[0]})
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(cmd, result)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Restored PDF %s: %s\n", result.DocumentID, result.Path)
		return nil
	}
	return command
}
