/* global tinymce */
/*
 * Ask Genie: a button of the blog editor that sends the selected text (or what
 * is typed in the box) with a task to the site's own /chat/completions, and
 * inserts the answer. The site's access token is used, so nothing is entered
 * here. The model and, for a model that thinks, the effort are chosen in the
 * dialog (js/model-choice.js, kept apart from the chat widget's choice by
 * window.MODEL_CHOICE_KEY); without it the server's default answers.
 *
 * Options (tinymce.init): openai: { api_key: <the page's access token>,
 * baseUri: "/chat/completions" }.
 */
tinymce.PluginManager.add('genie', function (editor) {
  var OPENAI = editor.getParam('openai') || {};

  var TASKS = [
    { text: 'Proofread', value: 'Proofread this text. Fix spelling, grammar and punctuation and keep the meaning and the voice.' },
    { text: 'Make it shorter', value: 'Rewrite this text so it is about half as long and keeps the main points.' },
    { text: 'Make it clearer', value: 'Rewrite this text so it is clearer and easier to read.' },
    { text: 'Summarize', value: 'Summarize this text in two or three sentences.' },
    { text: 'Continue writing', value: 'Continue this text with the next paragraph, in the same voice.', facts: true },
    { text: 'Write about this', value: 'Write a short blog post section about this topic.', facts: true }
  ];

  function escapeHtml(text) {
    return text.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
  }

  // plain text to paragraphs: a blank line starts a paragraph, a line break stays one
  function toParagraphs(text) {
    return text.trim().split(/\n{2,}/).map(function (p) {
      return '<p>' + escapeHtml(p).replace(/\n/g, '<br>') + '</p>';
    }).join('');
  }

  var CHOICE = window.ModelChoice || null;

  function modelOf(id) {
    return CHOICE && CHOICE.models.filter(function (m) { return m.id === id; })[0] || null;
  }

  // the title of the post being written, to search the site's notes with
  function postTitle() {
    var field = document.getElementById('blogtitle');
    return field ? field.value.trim() : '';
  }

  function ask(task, input) {
    var prompt = task + ' Answer with the text only, no introduction.\n\n' + input;
    var options = { baseUri: OPENAI.baseUri || '/chat/completions', model: 'gpt-4o-mini', temperature: 0.5, max_tokens: 1200 };
    // a thinking model counts its thinking against the answer, so it gets room for both
    var fields = CHOICE ? CHOICE.fields({ temperature: 0.5, maxTokens: 1200, extraTokens: 1200 })
                        : { model: options.model, temperature: options.temperature, max_tokens: options.max_tokens };
    // ask the server for what the site knows about itself (its notes and its
    // posts), searched with the title and the text, not the instructions
    // (only for tasks that write new text: proofreading or shortening a
    // paragraph needs no facts about the site)
    if (TASKS.some(function (t) { return t.value === task && t.facts; })) {
      fields.context = 'blog';
      fields.context_hint = (postTitle() + ' ' + input).trim().slice(0, 400);
    }
    options.fields = fields;
    return getResponseFromOpenAI(OPENAI.api_key, prompt, options).then(function (res) {
      return res.json().catch(function () { return {}; }).then(function (data) {
        if (!res.ok) {
          var why = data && data.error && data.error.message ? data.error.message : 'Genie could not answer (' + res.status + ').';
          throw new Error(why);
        }
        var message = data.choices && data.choices[0] && data.choices[0].message;
        if (!message || !message.content) throw new Error('Genie sent no answer.');
        return message.content;
      });
    });
  }

  function openDialog() {
    var selected = editor.selection.getContent({ format: 'text' }).trim();
    var items = [{ type: 'selectbox', name: 'task', label: 'What should Genie do?', items: TASKS.map(function (t) { return { text: t.text, value: t.value }; }) }];
    var initial = { task: TASKS[0].value, input: selected };
    if (CHOICE) {
      var now = CHOICE.get();
      items.push({ type: 'selectbox', name: 'model', label: 'Model', items: CHOICE.models.map(function (m) { return { text: m.name + (m.tag ? ' (' + m.tag.toLowerCase() + ')' : ''), value: m.id }; }) });
      items.push({ type: 'selectbox', name: 'effort', label: 'Effort (for models that think)', items: CHOICE.efforts.map(function (e) { return { text: e.label, value: e.id }; }) });
      initial.model = modelOf(now.model) ? now.model : CHOICE.models[0].id;
      initial.effort = now.effort;
    }
    items.push({ type: 'textarea', name: 'input', label: selected ? 'The selected text' : 'The text or topic' });
    // the effort applies to a model that thinks only
    function syncEffort(api) {
      if (!CHOICE) return;
      var m = modelOf(api.getData().model);
      api.setEnabled('effort', !!(m && m.reasoning));
    }
    var dialog = editor.windowManager.open({
      title: 'Ask Genie',
      body: {
        type: 'panel',
        items: items
      },
      buttons: [
        { type: 'cancel', text: 'Close' },
        { type: 'submit', text: 'Ask Genie', primary: true }
      ],
      initialData: initial,
      onChange: syncEffort,
      onSubmit: function (api) {
        var data = api.getData();
        if (CHOICE) CHOICE.set(data.model, data.effort);
        if (!data.input.trim()) {
          editor.notificationManager.open({ text: 'Select some text in the post, or type it here first.', type: 'warning', timeout: 3000 });
          return;
        }
        api.block('Genie is writing…');
        ask(data.task, data.input.trim()).then(function (answer) {
          api.unblock();
          editor.windowManager.open({
            title: 'Genie suggests',
            size: 'medium',
            body: { type: 'panel', items: [{ type: 'htmlpanel', html: '<div style="white-space:normal">' + toParagraphs(answer) + '</div>' }] },
            buttons: [
              { type: 'cancel', text: 'Discard' },
              { type: 'submit', text: selected ? 'Replace the selection' : 'Insert', primary: true }
            ],
            onSubmit: function (confirm) {
              editor.insertContent(toParagraphs(answer)); // replaces the selection, if there is one
              confirm.close();
              api.close();
            }
          });
        }, function (error) {
          api.unblock();
          editor.notificationManager.open({ text: String(error.message || error), type: 'error', timeout: 6000 });
        });
      }
    });
    syncEffort(dialog);
    return dialog;
  }

  editor.ui.registry.addIcon('genie', '<svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3l1.8 4.7L18.5 9.5l-4.7 1.8L12 16l-1.8-4.7L5.5 9.5l4.7-1.8z"/><path d="M19 15l.8 2.2L22 18l-2.2.8L19 21l-.8-2.2L16 18l2.2-.8z"/></svg>');

  editor.ui.registry.addButton('genie', {
    icon: 'genie',
    tooltip: 'Ask Genie',
    onAction: openDialog
  });
  editor.ui.registry.addMenuItem('genie', { icon: 'genie', text: 'Ask Genie', onAction: openDialog });

  return { getMetadata: function () { return { name: 'Ask Genie' }; } };
});
