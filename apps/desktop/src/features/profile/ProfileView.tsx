import { FileText, MapPin, Plus, Target } from "lucide-react";

export function ProfileView() {
  return (
    <section className="workspace simple-workspace">
      <div className="workspace-header">
        <div>
          <h1>Perfil</h1>
          <p>Informações usadas para buscar e analisar vagas.</p>
        </div>
        <button className="primary-button" type="button"><Plus size={17} />Criar perfil</button>
      </div>
      <div className="settings-grid">
        <button className="setting-row" type="button"><Target size={19} /><span><strong>Cargos e senioridade</strong><small>Não configurado</small></span></button>
        <button className="setting-row" type="button"><MapPin size={19} /><span><strong>Localização e modalidade</strong><small>Não configurado</small></span></button>
        <button className="setting-row" type="button"><FileText size={19} /><span><strong>Currículo</strong><small>Nenhum arquivo adicionado</small></span></button>
      </div>
    </section>
  );
}

