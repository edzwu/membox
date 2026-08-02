package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"membox"
)

func newTopicCommand(runtime *runtime) *cobra.Command {
	topic := parentCommand("topic", "Manage document topics", "a topic command is required")
	topic.AddCommand(newTopicCreateCommand(runtime), newTopicListCommand(runtime), newTopicAddCommand(runtime), newTopicRemoveCommand(runtime), newTopicDocumentsCommand(runtime))
	return topic
}

func newTopicCreateCommand(runtime *runtime) *cobra.Command {
	var jsonOutput bool
	command := &cobra.Command{Use: "create <name>", Short: "Create a topic", Args: exactArgs(1, "topic name")}
	command.Flags().BoolVar(&jsonOutput, "json", false, "output JSON")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		box, err := runtime.get()
		if err != nil {
			return err
		}
		result, err := box.CreateTopic(cmd.Context(), membox.CreateTopicCommand{Name: args[0]})
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(cmd, result)
		}
		if result.AlreadyExists {
			fmt.Fprintf(cmd.OutOrStdout(), "Topic already exists: %s\n", result.Topic.Name)
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "Created topic: %s\n", result.Topic.Name)
		}
		return nil
	}
	return command
}

func newTopicListCommand(runtime *runtime) *cobra.Command {
	var jsonOutput bool
	command := &cobra.Command{Use: "list", Short: "List topics", Args: noArgs}
	command.Flags().BoolVar(&jsonOutput, "json", false, "output JSON")
	command.RunE = func(cmd *cobra.Command, _ []string) error {
		box, err := runtime.get()
		if err != nil {
			return err
		}
		topics, err := box.ListTopics(cmd.Context(), membox.ListTopicsQuery{})
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(cmd, topics)
		}
		if len(topics) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No topics.")
			return nil
		}
		for _, topic := range topics {
			fmt.Fprintf(cmd.OutOrStdout(), "%s  %s  %s\n", shortID(topic.ID), topic.Name, topic.Path)
		}
		return nil
	}
	return command
}

func newTopicAddCommand(runtime *runtime) *cobra.Command {
	var jsonOutput bool
	command := &cobra.Command{Use: "add <topic-id> <document-id>", Short: "Add a document to a topic", Args: exactArgs(2, "topic and document selectors")}
	command.Flags().BoolVar(&jsonOutput, "json", false, "output JSON")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		box, err := runtime.get()
		if err != nil {
			return err
		}
		result, err := box.AddDocumentTopic(cmd.Context(), membox.TopicMembershipCommand{DocumentSelector: args[1], TopicSelector: args[0]})
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(cmd, result)
		}
		if result.Added {
			fmt.Fprintf(cmd.OutOrStdout(), "Added document to topic %q.\n", result.Topic.Name)
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "Document is already in topic %q.\n", result.Topic.Name)
		}
		return nil
	}
	return command
}

func newTopicRemoveCommand(runtime *runtime) *cobra.Command {
	var jsonOutput bool
	command := &cobra.Command{Use: "remove <topic-id> <document-id>", Short: "Remove a document from a topic", Args: exactArgs(2, "topic and document selectors")}
	command.Flags().BoolVar(&jsonOutput, "json", false, "output JSON")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		box, err := runtime.get()
		if err != nil {
			return err
		}
		result, err := box.RemoveDocumentTopic(cmd.Context(), membox.TopicMembershipCommand{DocumentSelector: args[1], TopicSelector: args[0]})
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(cmd, result)
		}
		if result.Removed {
			fmt.Fprintf(cmd.OutOrStdout(), "Removed document from topic %q.\n", result.Topic.Name)
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "Document was not in topic %q.\n", result.Topic.Name)
		}
		return nil
	}
	return command
}

func newTopicDocumentsCommand(runtime *runtime) *cobra.Command {
	var jsonOutput bool
	command := &cobra.Command{Use: "documents <topic>", Short: "List documents in a topic", Args: exactArgs(1, "topic selector")}
	command.Flags().BoolVar(&jsonOutput, "json", false, "output JSON")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		box, err := runtime.get()
		if err != nil {
			return err
		}
		result, err := box.ListTopicDocuments(cmd.Context(), membox.ListTopicDocumentsQuery{Selector: args[0]})
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(cmd, result)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Topic %s: %d document(s)\n", result.Topic.Name, len(result.Documents))
		for _, document := range result.Documents {
			fmt.Fprintf(cmd.OutOrStdout(), "  %s  %s  %s\n", shortID(document.ID), displayName(document.Title, document.Path), document.Path)
		}
		return nil
	}
	return command
}
