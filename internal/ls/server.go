package ls

import (
	"log/slog"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
	"github.com/tliron/glsp/server"
)

var (
	ServerName = "graphql-language-server"
	Version    = "dev"
)

type Server struct {
	handler protocol.Handler
	state   *State
}

func New() *Server {
	s := &Server{
		state: newState(),
	}
	s.handler = protocol.Handler{
		Initialize:             s.initialize,
		Shutdown:               s.shutdown,
		SetTrace:               s.setTrace,
		TextDocumentDidOpen:    s.didOpen,
		TextDocumentDidChange:  s.didChange,
		TextDocumentDidClose:   s.didClose,
		TextDocumentDidSave:    s.didSave,
		TextDocumentHover:      s.hover,
		TextDocumentDefinition: s.definition,
		TextDocumentReferences: s.references,
		TextDocumentCompletion: s.completion,
	}
	return s
}

func (s *Server) RunStdio() error {
	slog.Debug("starting LSP server", "name", ServerName, "version", Version)
	srv := server.NewServer(&s.handler, ServerName, false)
	return srv.RunStdio()
}

func (s *Server) initialize(_ *glsp.Context, params *protocol.InitializeParams) (any, error) {
	slog.Debug("initialize request received")
	capabilities := s.handler.CreateServerCapabilities()
	syncKind := protocol.TextDocumentSyncKindFull
	capabilities.TextDocumentSync = &protocol.TextDocumentSyncOptions{
		OpenClose: &protocol.True,
		Change:    &syncKind,
	}
	capabilities.TextDocumentSync.(*protocol.TextDocumentSyncOptions).Save = &protocol.True
	capabilities.CompletionProvider = &protocol.CompletionOptions{
		TriggerCharacters: []string{"@", ":", " "},
	}

	rootPath := ""
	if params.RootURI != nil {
		rootPath = uriToPath(*params.RootURI)
	} else if params.RootPath != nil {
		rootPath = *params.RootPath
	}
	schemaPaths := readInitializationOptions(params.InitializationOptions)
	s.state.mu.Lock()
	s.state.rootPath = rootPath
	s.state.schemaPaths = schemaPaths
	s.state.mu.Unlock()
	slog.Debug("initialize configuration", "rootPath", rootPath, "schemaPaths", schemaPaths)

	return protocol.InitializeResult{
		Capabilities: capabilities,
		ServerInfo: &protocol.InitializeResultServerInfo{
			Name:    ServerName,
			Version: &Version,
		},
	}, nil
}

func (s *Server) shutdown(_ *glsp.Context) error {
	slog.Debug("shutdown request received")
	protocol.SetTraceValue(protocol.TraceValueOff)
	return nil
}

func (s *Server) setTrace(_ *glsp.Context, params *protocol.SetTraceParams) error {
	slog.Debug("setTrace request received", "value", params.Value)
	protocol.SetTraceValue(params.Value)
	return nil
}

func (s *Server) didOpen(ctx *glsp.Context, params *protocol.DidOpenTextDocumentParams) error {
	slog.Debug("didOpen", "uri", params.TextDocument.URI, "version", params.TextDocument.Version)
	s.state.mu.Lock()
	s.state.docs[params.TextDocument.URI] = params.TextDocument.Text
	s.state.mu.Unlock()

	s.publishQueryDiagnostics(ctx, params.TextDocument.URI, params.TextDocument.Text)
	s.loadWorkspaceSchema(ctx)
	return nil
}

func (s *Server) didChange(ctx *glsp.Context, params *protocol.DidChangeTextDocumentParams) error {
	if len(params.ContentChanges) == 0 {
		return nil
	}

	s.state.mu.Lock()
	current := s.state.docs[params.TextDocument.URI]
	s.state.mu.Unlock()

	text, ok := applyContentChanges(current, params.ContentChanges)
	if !ok {
		slog.Debug("didChange: unsupported change payload", "uri", params.TextDocument.URI)
		return nil
	}
	logChangeSummary(params.TextDocument.URI, params.TextDocument.Version, params.ContentChanges, len(text))

	s.state.mu.Lock()
	s.state.docs[params.TextDocument.URI] = text
	s.state.mu.Unlock()

	s.publishQueryDiagnostics(ctx, params.TextDocument.URI, text)
	s.loadWorkspaceSchema(ctx)
	return nil
}

func (s *Server) didClose(ctx *glsp.Context, params *protocol.DidCloseTextDocumentParams) error {
	slog.Debug("didClose", "uri", params.TextDocument.URI)
	s.state.mu.Lock()
	delete(s.state.docs, params.TextDocument.URI)
	delete(s.state.queryDiagnostics, params.TextDocument.URI)
	s.state.mu.Unlock()

	s.loadWorkspaceSchema(ctx)
	s.publishCombinedDiagnostics(ctx, params.TextDocument.URI)
	return nil
}

func (s *Server) didSave(ctx *glsp.Context, params *protocol.DidSaveTextDocumentParams) error {
	slog.Debug("didSave", "uri", params.TextDocument.URI)
	s.loadWorkspaceSchema(ctx)
	return nil
}
