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

export type AssistantConfirmTaskResult = {
  error_message?: string | null;
  messages: AssistantMessage[];
  session: AssistantSession;
  task: AssistantTaskSummary;
};
