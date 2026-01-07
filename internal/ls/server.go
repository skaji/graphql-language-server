package ls

import (
	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
	"github.com/tliron/glsp/server"
)

var (
	ServerName = "graphql-language-server"
	Version    = "0.0.1"
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
		TextDocumentHover:      s.hover,
		TextDocumentDefinition: s.definition,
	}
	return s
}

func (s *Server) RunStdio() error {
	srv := server.NewServer(&s.handler, ServerName, false)
	return srv.RunStdio()
}

func (s *Server) initialize(_ *glsp.Context, params *protocol.InitializeParams) (any, error) {
	capabilities := s.handler.CreateServerCapabilities()
	syncKind := protocol.TextDocumentSyncKindFull
	capabilities.TextDocumentSync = &protocol.TextDocumentSyncOptions{
		OpenClose: &protocol.True,
		Change:    &syncKind,
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

	return protocol.InitializeResult{
		Capabilities: capabilities,
		ServerInfo: &protocol.InitializeResultServerInfo{
			Name:    ServerName,
			Version: &Version,
		},
	}, nil
}

func (s *Server) shutdown(_ *glsp.Context) error {
	protocol.SetTraceValue(protocol.TraceValueOff)
	return nil
}

func (s *Server) setTrace(_ *glsp.Context, params *protocol.SetTraceParams) error {
	protocol.SetTraceValue(params.Value)
	return nil
}

func (s *Server) didOpen(context *glsp.Context, params *protocol.DidOpenTextDocumentParams) error {
	s.state.mu.Lock()
	s.state.docs[params.TextDocument.URI] = params.TextDocument.Text
	s.state.mu.Unlock()

	s.publishQueryDiagnostics(context, params.TextDocument.URI, params.TextDocument.Text)
	s.loadWorkspaceSchema(context)
	return nil
}

func (s *Server) didChange(context *glsp.Context, params *protocol.DidChangeTextDocumentParams) error {
	if len(params.ContentChanges) == 0 {
		return nil
	}

	var text string
	switch change := params.ContentChanges[len(params.ContentChanges)-1].(type) {
	case protocol.TextDocumentContentChangeEventWhole:
		text = change.Text
	case protocol.TextDocumentContentChangeEvent:
		text = change.Text
	default:
		return nil
	}

	s.state.mu.Lock()
	s.state.docs[params.TextDocument.URI] = text
	s.state.mu.Unlock()

	s.publishQueryDiagnostics(context, params.TextDocument.URI, text)
	s.loadWorkspaceSchema(context)
	return nil
}

func (s *Server) didClose(context *glsp.Context, params *protocol.DidCloseTextDocumentParams) error {
	s.state.mu.Lock()
	delete(s.state.docs, params.TextDocument.URI)
	delete(s.state.queryDiagnostics, params.TextDocument.URI)
	s.state.mu.Unlock()

	s.loadWorkspaceSchema(context)
	s.publishCombinedDiagnostics(context, params.TextDocument.URI)
	return nil
}
