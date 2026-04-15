export type AssistantSession = {
  created_at: string;
  id: string;
  last_message_at: string;
  title: string;
  updated_at: string;
};

export type AssistantTextPayload = {
  content: string;
};

export type AssistantTaskSuggestionPayload = {
  action_label: string;
  can_create: boolean;
  instruction: string;
  resource_id?: string;
  resource_label: string;
  status_message: string;
  title: string;
};

export type AssistantTaskCreatedPayload = {
  detail_url: string;
  instruction: string;
  resource_id: string;
  status: string;
  suggestion_message_id: string;
  task_id: string;
};

export type AssistantSessionFilePayload = {
  file_id?: string;
  file_name: string;
  resource_id: string;
  resource_title: string;
  source_type: string;
  status: string;
};

export type AssistantSystemPayload = {
  content: string;
  level: string;
};

type AssistantMessageBase = {
  created_at: string;
  id: string;
  role: "assistant" | "user";
  sequence_no: number;
};

export type AssistantTextMessage = AssistantMessageBase & {
  kind: "text";
  payload: AssistantTextPayload;
};

export type AssistantTaskSuggestionMessage = AssistantMessageBase & {
  kind: "task_suggestion";
  payload: AssistantTaskSuggestionPayload;
};

export type AssistantTaskCreatedMessage = AssistantMessageBase & {
  kind: "task_created";
  payload: AssistantTaskCreatedPayload;
};

export type AssistantSessionFileMessage = AssistantMessageBase & {
  kind: "session_file";
  payload: AssistantSessionFilePayload;
};

export type AssistantSystemMessage = AssistantMessageBase & {
  kind: "system";
  payload: AssistantSystemPayload;
};

export type AssistantMessage =
  | AssistantSessionFileMessage
  | AssistantSystemMessage
  | AssistantTaskCreatedMessage
  | AssistantTaskSuggestionMessage
  | AssistantTextMessage;

export type AssistantConversation = {
  messages: AssistantMessage[];
  session: AssistantSession;
};

export type AssistantResourceSummary = {
  id: string;
  source_type: string;
  title: string;
} | null;

export type AssistantTaskSummary = {
  created_at: string;
  id: string;
  instruction: string;
  resource_id: string;
  status: string;
} | null;

export type AssistantUploadResult = {
  error_message?: string | null;
  messages: AssistantMessage[];
  resource: AssistantResourceSummary;
  session: AssistantSession;
};

export type AssistantUploadCapabilities = {
  accept: string;
  hint: string;
  supported_extensions: string[];
};

export type AssistantCapabilities = {
  upload: AssistantUploadCapabilities;
};

export type AssistantConfirmTaskResult = {
  error_message?: string | null;
  messages: AssistantMessage[];
  session: AssistantSession;
  task: AssistantTaskSummary;
};

export type AssistantTurnErrorCode =
  | "assistant_empty_reply"
  | "backend_offline"
  | "generation_stopped"
  | "request_timeout"
  | "service_error";

export type AssistantTurnError = {
  code: AssistantTurnErrorCode;
  message: string;
};

export type AssistantStreamEvent =
  | {
      session: AssistantSession;
      type: "session_created";
    }
  | {
      type: "message_started";
    }
  | {
      delta: string;
      type: "message_delta";
    }
  | {
      message: AssistantMessage;
      type: "message_completed";
    }
  | {
      message: AssistantTaskSuggestionMessage;
      type: "task_suggestion";
    }
  | {
      error: AssistantTurnError;
      type: "error";
    }
  | {
      type: "done";
    };

export type AssistantStreamRunResult =
  | {
      status: "completed";
    }
  | {
      error: AssistantTurnError;
      status: "stopped";
    };

type AssistantLocalMessageBase = {
  created_at: string;
  id: string;
  role: "assistant" | "user";
  sequence_no: number;
};

export type AssistantLocalTextMessage = AssistantLocalMessageBase & {
  kind: "local_text";
  local_state?: "streaming";
  payload: AssistantTextPayload;
};

export type AssistantLocalErrorMessage = AssistantLocalMessageBase & {
  kind: "local_error";
  payload: {
    code: AssistantTurnErrorCode;
    content: string;
  };
  role: "assistant";
};

export type AssistantRenderableMessage =
  | AssistantLocalErrorMessage
  | AssistantLocalTextMessage
  | AssistantMessage;
