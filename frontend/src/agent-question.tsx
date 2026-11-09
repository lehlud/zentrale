import { Button, FieldError, Label, TextArea, TextField } from "@heroui/react";
import { useState } from "react";

import { useAPI } from "./api";
import type { Question } from "./generated";
import { useI18n } from "./preferences";
import { Failure, useAction } from "./ui";

export function AgentQuestion({ question }: { question: Question }) {
  const { t } = useI18n();

  const api = useAPI();
  const action = useAction();
  const [answer, setAnswer] = useState("");
  const respond = (text: string, status = "answered") =>
    action.run(() =>
      api.mutate(
        "mutation($id:String!,$answer:String!,$status:String!){answerQuestion(id:$id,answer:$answer,status:$status)}",
        { id: question.id, answer: text, status },
      ),
    );

  return (
    <section
      aria-labelledby={`question-${question.id}`}
      className="my-5 space-y-4 border-l-2 border-accent py-3 pl-4"
    >
      <p className="font-mono text-xs text-accent">{t("Rückfrage")}</p>
      <h3 id={`question-${question.id}`} className="text-sm font-medium">
        {question.title}
      </h3>
      <div className="flex flex-wrap gap-2">
        {question.options.map((option) => (
          <Button
            className="h-auto min-h-11 max-w-full whitespace-normal py-2 text-left"
            key={option}
            size="sm"
            variant="secondary"
            isDisabled={action.busy}
            onPress={() => respond(option)}
          >
            {option}
          </Button>
        ))}
      </div>
      <TextField className="min-w-0 gap-2" value={answer} onChange={setAnswer}>
        <Label>{t("Eigene Antwort")}</Label>
        <TextArea className="min-h-11" rows={1} />
        <FieldError></FieldError>
      </TextField>
      <Failure message={action.error} />
      <div className="flex gap-2">
        <Button
          className="min-h-11"
          size="sm"
          isDisabled={!answer.trim() || action.busy}
          onPress={() => respond(answer)}
        >
          {t("Antworten")}
        </Button>
        <Button
          className="min-h-11"
          size="sm"
          variant="ghost"
          isDisabled={action.busy}
          onPress={() => respond("", "dismissed")}
        >
          {t("Verwerfen")}
        </Button>
      </div>
    </section>
  );
}
